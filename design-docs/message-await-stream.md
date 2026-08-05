# `message await` / `message stream`

Live message and reaction delivery over Slack's event socket. The transport and
the frame shapes it delivers are documented in `behavior-reference.md` ("The
event socket"); this file is the command design built on top of them.

## Why

An agent that posts a question has no way to collect the answer. Today the only
option is a polling loop over `message list`, which is slow, spends rate-limit
budget, and races: a reply landing between the send and the next poll is
invisible until the poll after it. Slack's own client does not poll — it renders
from a WebSocket — and that socket is reachable with the browser credentials we
already hold, with no subscription and no rate limit.

The second, less obvious motivation is human-in-the-loop approval. `reaction_added`
arrives on the same socket, so "post a proposal and block until a human reacts"
becomes a single command rather than a polling loop with a bespoke matcher.

## Two commands, one record

`message await` returns **one** event and exits (single resource → JSON).
`message stream` emits **many** (list → NDJSON). They share a filter engine and
emit the identical event record; `await` simply wraps it:

```json
{ "received": true, "cursor": "…", "waited_ms": 47120, "event": { … } }
```

The wrapped object is byte-identical to a `stream` line, so one parser serves
both. A caller must not have to branch on a discriminator to find the payload.

### The event record

Field names reuse `render.CompactMessage` so a stream line parses like a
`message list` line, with an added `event` discriminator:

```json
{"event":"message","channel_id":"C…","ts":"…","thread_ts":"…",
 "author":{"user_id":"U…"},"content":"…"}
```

| `event` | Extra fields | Notes |
|---|---|---|
| `message` | as `message list` | includes bot posts; `author.bot_id` when the author is an app |
| `reaction_added` / `reaction_removed` | `reaction`, `ts` is the *target message's* | `event_ts` is when the reaction happened |
| `message_changed` | `content` (new), `previous_content` | `ts` is the edited message's |
| `message_deleted` | — | `ts` is the deleted message's |

## Behavior that the frame shapes force

The socket is a firehose of the user's whole Slack — roughly fifteen
bookkeeping frames per real message — so filtering is the engine, not a flag.
Three shapes would corrupt a naive consumer and are handled centrally
(`mockslack.DefaultEventScript` models each):

- **A thread reply arrives twice**: once as the reply, once as `message_replied`
  re-sending the *parent*. `message_replied` is always dropped; forwarding it
  re-emits the parent as though newly posted.
- **A bot message has no `user`** — the author is `bot_id`/`username`. Bot posts
  count as messages by default; an agent usually waits on app output.
- **Edits and deletes are message-typed with `hidden: true`** and no text of
  their own. They are separate event kinds, never `message`.

Everything else — `im_marked`, `badge_counts_updated`,
`clear_mention_notification`, `update_global_thread_state`, `thread_subscribed`,
`user_invalidated`, `dnd_invalidated`, `activity/*`, `file_view_ready`,
`desktop_notification`, `user_typing`, `presence_change` — is dropped. None of
it is new activity, and `desktop_notification` duplicates content the message
frame already delivered.

## `--since`, and why ordering matters

`--since <ts>` means *strictly after this timestamp*, and it exists to close the
gap between sending and waiting:

```
T0  message send   → ts 1785…100
T1  (agent works — the reply lands here)
T2  message await  → without --since, blocks forever
```

The value is the `ts` a send returned, or the `cursor` a previous call returned.
It is **exclusive** — the ts passed is almost always the caller's own message.
This differs from `message list --oldest`, which is an inclusive range bound;
the names stay distinct for that reason.

Implementation order is load-bearing: **attach the socket first, then backfill,
then dedupe by `(channel_id, ts)`**. Backfilling first reopens the gap in
miniature — anything landing between the history response and the upgrade is
lost. This is the same listen-before-act ordering as `awaitOpenedView`.

The returned `cursor` is the last ts actually examined, never "now". With no
events at all it echoes the input unchanged; advancing past unexamined time
would silently create the gap the flag exists to prevent.

## Filters, and never swallowing a "no"

A filter that hides a rejection turns it into a timeout, and an agent cannot
distinguish *rejection* from *silence* — the difference between "stop" and
"retry". So anything the filters exclude **within the target** is reported back
in `skipped` (bounded at 20 entries):

```json
{ "received": false, "cursor": "…", "waited_ms": 1800000,
  "skipped": [ { "event": "reaction_added", "reaction": "x", "author": { "user_id": "U…" } } ] }
```

This applies to every filter, not just `--reaction`: `--from` has the same
failure mode when someone else answers.

`--events` is **not** one of those filters. Kind is a primary selector, not a
narrowing filter: a caller awaiting a reaction was never a candidate for the
channel's messages, and reporting them would bury the one event that matters.

For the same reason the **default is to match any reaction** and report its
name. Approval is expressed as ✅ ✔️ ☑️ 👍 🎉 or a reply; no fixed list is
right, and judging intent is the calling model's job, not a string comparison.
`--reaction` narrows for callers who genuinely want one emoji, applying the
mechanical normalizations: skin-tone modifiers stripped (`+1::skin-tone-3` →
`+1`) and workspace aliases resolved one hop.

## Channel targets and thread replies

`message await #deploys` excludes replies inside existing threads by default,
matching what `message list #deploys` shows — two commands disagreeing about
what "a message in this channel" means is exactly the inconsistency that burns
an agent. `--include-thread-replies` opts in. A permalink or `--thread-ts`
target awaits *within* that thread, where replies are the whole point.

## Reconnection

Drops are expected on a 30-minute await. The engine reconnects transparently:
dial the pushed `reconnect_url` (falling back to a fresh `client.getWebSocketURL`),
gap-fill from the last cursor via `conversations.history`, dedupe by
`(channel_id, ts)`, continue. A reconnect is never surfaced as an error; it
appears only in `--debug` output and in the stream's `@summary`.

## Standard tokens

`client.getWebSocketURL` is a client API, so the socket needs browser auth.
`await` falls back to polling `conversations.history` with a stderr notice —
degraded but honest, and a 5-minute wait is fine even on the 1 req/min tier.
`stream` refuses: polling every conversation in a workspace is not viable.

## Bounds

Neither command may run unbounded — an agent shelling out needs a process that
returns. `await` takes `--timeout` (default 5m). `stream` takes `--duration`
(default 10m), `--max-events`, and `--idle-timeout`. Every bound exits 0 with a
cursor; only auth, target, and transport failures exit non-zero.

`stream`'s `@summary` carries **per-channel** cursors, because gap-fill is per
conversation — a single scalar cursor across channels is not a valid resume
point. `await` never has this problem: it is always scoped to one conversation.

For that reason `stream` has no `--since`: resuming N conversations from one
scalar would fan out into an unbounded backfill at startup. It starts live and
reports where it got to.
