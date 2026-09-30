---
name: agent-slack
description: |
  Read and act on Slack from the terminal: permalinks, threads, history,
  unreads, saved-for-later, canvases, and workflows; message and file
  search; attachment downloads; user lookup; channel listing and creation;
  DMs; sending, editing, deleting, scheduling, and reacting; and raw Slack
  API calls. Use when asked to fetch a Slack message URL, read a thread or
  channel, check unreads, search Slack, post or edit a message, invite
  someone to a channel, or run a workflow.
allowed-tools: Bash(agent-slack *) Read
---

# agent-slack

JSON in, JSON out, no interactivity. Lists are NDJSON (one object per line, then
`{"@pagination":…}` / `{"@referenced_users":…}` meta lines). Entity gets —
`user get`, `channel get`, `usergroup get`, `emoji get` — accept 1..N ids and
emit NDJSON by default: one result line per id in input order, either the record
or `{"@unresolved":{"id","reason","fixable_by","hint"?}}` for an id that
couldn't be resolved. Item-level misses exit 0; only a command-level failure
(auth, network) exits 1. `--format json` on a single get returns the pretty
object; `--format json` on multi collapses to `{"data":[…],"@unresolved":[…]}`.
`message get`, `message draft get`, `workflow get`, and `canvas get` are single-arg
and now emit NDJSON by default (one line); `--format json|yaml` returns the pretty
object. `config get` takes 1..N keys and emits NDJSON (one line per key, or
`{"@unresolved":…}` per miss). Errors are a single
JSON object on stderr:
`{"error":"…","fixable_by":"agent|human|retry","hint"?:"…","retry_after_seconds"?:N}`.
`fixable_by=agent` → fix the input and retry; `human` → credentials/permissions
need a person; `retry` → transient failure, wait and re-run (`retry_after_seconds`
gives the recommended back-off when present).

**Safety.** Read and search freely. Do not send, edit, delete, react, schedule,
invite, create channels, or add/remove emoji unless the user explicitly asked
for that action. Destructive commands — `message edit|delete`, `message draft
delete`, `message scheduled cancel`, `channel new|invite`, `emoji add|remove` —
require `--yes`; without it they return a description of what *would* happen.
Show that to the user before retrying with `--yes`.

This page covers the common paths inline. Every domain has complete, always-
current detail in `agent-slack <domain> usage` — pull that (or the bundled
references at the bottom) when a task needs a domain not shown here.

## Setup (once)

```bash
agent-slack auth import-desktop            # from Slack Desktop — best, no need to quit
# …or, if you don't run Slack Desktop:
agent-slack auth import-browser firefox    # chrome|brave|firefox|zen|opera|safari
agent-slack auth test                      # am I set up? → who I am + which workspace
agent-slack auth list                      # configured credential sets (aliases)
agent-slack auth set-default acme          # if several workspaces (alias or URL)
```

If no import works, `agent-slack auth add --workspace-url <url> --form` opens a
native OS dialog so the human enters the token without it appearing in chat.
Never ask the user to paste a token into the conversation, and never read
credentials out of the store yourself; every command authenticates internally.
Env override: `SLACK_TOKEN` (+ `SLACK_COOKIE_D` + `SLACK_WORKSPACE_URL` for xoxc
browser tokens); expired browser tokens self-heal from Slack Desktop. With
several workspaces, commands use the default; pass `--workspace <alias>` (or any
unique substring) to target another (a message permalink carries its own
workspace and overrides it). Credential sets are alias-keyed — several aliases
may hold the same workspace URL (e.g. two humans in one Slack).
Full menu — per-browser caveats, bot tokens, cURL import: `agent-slack auth usage`.

## Reading

```bash
agent-slack message get "https://acme.slack.com/archives/C…/p1770165109628379"   # quote it — a bare & truncates in the shell
agent-slack message get "#general" --ts "1770165109.628379"
agent-slack message list "#general" --limit 25
agent-slack message list "#general" --thread-ts "1770165109.628379"   # whole thread
agent-slack unreads --counts-only
```

`message get` returns a `permalink`, a `thread` summary `{ts,length}`, and
downloads attachments (local paths in `files[].path`; `--no-download` to skip;
lists stay metadata-only — add `--download` or `agent-slack file download F…`).
Reads resolve referenced users/channels/usergroups to profiles **by default**
(`referenced_*` maps); tune with `--resolve none|cached|auto|fresh`.
`--include-reactions` adds reactions. Targeting rules (permalink vs `#channel`
vs `U…` DM, `--ts`): [references/targets.md](references/targets.md). These are
the common reads; reaction filters, body-length caps, and the full flag set are
in `agent-slack message usage`.

The message body is the **`content`** field — one rendered-Markdown string
merging Slack's raw `text`, blocks, and attachment/app-card unfurls. There is
no `text` field in output. A row without `content` genuinely has no text body;
re-fetching another way won't reveal more (`--full` shows the raw payload if
you must check).

**Files over MCP (`agent-slack mcp`):** an MCP client has no filesystem, so the
local `path`s above come back as fetchable references
(`{"@type":"file","root":"cache","path":"<team_id>/<user_id>/downloads/F….png"}`)
and you read them with the bridge's built-in **`fs`** tool — no host path
needed. Pass the `path` from the reference verbatim; downloads nest under the
identity (`<team_id>/<user_id>/downloads/`), so don't assume a bare
`downloads/` prefix:

```text
fs get  cache <team_id>/<user_id>/downloads/F0BD….png   # returns the bytes (images inline as image blocks)
fs find cache -e png                                     # discover downloaded images
fs ls   cache <team_id>/<user_id>/downloads              # list a directory
```

`get` refuses files over a small inline limit. In plain-CLI use the `path`s are
real and Read-able directly. Detail: [references/commands/message.md](references/commands/message.md).

## Searching

```bash
agent-slack search messages "deploy failed" --channel "#ops" --after 2026-06-01
agent-slack search files "architecture diagram" --content-type image
```

File hits download automatically and report local `path`s you can Read.

## Writing

```bash
agent-slack message send "#general" "ship it :rocket: — [release notes](https://acme.com/releases/4.2)"
agent-slack message send U05BRPTKL6A "ping"                  # DM auto-opens
agent-slack message send "<permalink>" "see the [run](https://ci.acme.com/123)"   # reply in thread
agent-slack message send "#general" "see attached" --attach ./report.md
agent-slack message react add "<permalink>" :eyes:
agent-slack message edit "<permalink>" "fixed wording" --yes        # edit/delete gated
agent-slack message delete "<permalink>" --yes
```

Message text is standard Markdown — `**bold**`, `*italic*`/`_italic_`,
`~~strike~~`, `` `code` ``, fenced code, `- bullets`, `1. numbers`, `> quotes`.
Indent a list item to nest it (any depth; bullets and numbers mix freely, and a
sub-list doesn't restart the numbering above it).
**Links:** write `[label](https://…)` for a labeled hyperlink; an unlabeled
`[url](url)` or `<url>` renders as Slack's inline link chip (the scheme-stripped
pill the composer makes from a pasted URL). Don't drop a truly bare URL in and
hope it renders nicely — it won't auto-link.
Two gotchas: `__text__` is **underline** (our extension, not bold) and `\*`
escapes a literal marker. Mentions auto-resolve: `@here`/`@channel`, `@U…` ids,
and bare `@name`/`@group` handles. Pass `--slack-markdown` for Slack's native
mrkdwn (`*bold*`, `<url|label>`); reads return Markdown too. **Read
[references/formatting.md](references/formatting.md) before composing any
formatted message** — full table of links, mentions, escaping, and the
`--slack-markdown` dialect.
Scheduling, forwarding, and the draft hand-off flow: `agent-slack message usage`.

## Waiting for a reply

```bash
ts=$(agent-slack message send "#team" "deploy blocked — proceed or hold?" | jq -r .ts)
agent-slack message await "#team" --since "$ts" --events message,reaction --timeout 30m
```

**That is the pattern for asking a human something.** A person answers in one
of three ways — a message in the channel, a reply threaded on your message, or
just an emoji reaction on it — and you cannot predict which. The call above
catches all three, including an answer that landed before `await` started:
`--since` is exclusive and catches up from there. Reactions are opt-in via
`--events`; leave the name unfiltered and judge it yourself (✅ ✔️ ☑️ 👍 🎉,
a custom `:approved:`, or a plain "yes"). If you narrow with `--reaction`, read
`skipped` — it carries the ❌ that would otherwise look like silence.

A timeout is **not an error**: exit 0, `{"received": false}`, and a `cursor` to
pass as the next `--since`, so looping loses nothing.

## Holding a conversation

Ask → answer → reply in the thread → wait → … is a loop over one thread root.
Two rules keep it from losing answers:

- **`--conversation <root ts>`** names the message that started it, the same
  value every turn. Replies in its thread, messages in the channel, and
  reactions on the root or on any of your messages in it all count. Without
  it, from the second turn on an in-thread answer is missed.
- **`--since` is always the previous result's `cursor`** — never your own
  reply's ts, which would skip whatever arrived while you were writing it.

**Listen in the background when you can.** You cannot know whether the answer
comes in a minute or in three hours, so don't block your session on it:

1. **Best — one stream for the whole conversation.** If your harness can run a
   command in the background and wake you as it prints lines, run one
   `stream` and keep it alive across turns. It never stops listening, so
   nothing lands between turns:

   ```bash
   root=$(agent-slack message send "#team" "deploy blocked — proceed or hold?" | jq -r .ts)
   agent-slack message stream --channel "#team" --conversation "$root" --since "$root" \
     --events message,reaction --duration 4h --idle-timeout 1h     # in the background
   agent-slack message send "#team" "holding — will retry at 3" --thread-ts "$root"
   ```

2. **Otherwise — `await` in the background**, with a long `--timeout`, and
   start the next one as soon as you have handled the result:

   ```bash
   agent-slack message await "#team" --conversation "$root" --since "$cursor" \
     --events message,reaction --timeout 2h
   ```

3. Block in the foreground only if you have no background option, with a
   shorter `--timeout` looped on `cursor`.

**Judge every event.** A channel message may be unrelated chatter, and 👀 means
"seen", not "yes". When it is not an answer, wait again from its `cursor`. A
reaction that landed before the wait began comes back with `caught_up: true`
and the message's full `reactions` list — read them all, as the others are
not delivered separately. Between two `await`s, a reaction on one of your
*older* messages can be missed (history cannot date it); the stream has no
such gap.

**Where to reply.** In the thread (`--thread-ts "$root"`) by default. People
often answer in the channel instead, especially while the thread is still near
the bottom (fewer than ~5 channel messages since the root) — to them it is one
conversation, and they may not be following the thread. Then reply in the
channel too. If the channel has moved on, reply in the thread and `@mention`
them so it reaches them. Either way keep `--conversation` unchanged: the
channel is already in scope.

## Watching a channel

```bash
agent-slack message stream --channel "#alerts" --duration 30m --idle-timeout 10m
```

Deploys, alerts, an incident room: NDJSON, one event per line, `@summary` with
per-channel cursors at the end. Always bounded, so it returns.

App posts count as messages and carry `author.bot_id` with **no**
`author.user_id` — most alert traffic is apps, so never key on `user_id` alone.
Both commands drop the socket's bookkeeping noise (typing, read marks, badges)
and never re-emit a thread's parent when a reply arrives. Neither polls or
spends rate-limit budget. `stream` needs browser auth; `await` falls back to
polling on a bot token (messages only). Full flags: [references/commands/message.md](references/commands/message.md).

## Finding people & channels

```bash
agent-slack channel list                      # compact; --full for raw
agent-slack channel get "#general" "#ops"     # NDJSON default (one line per id; @unresolved for misses)
agent-slack channel members "#general" --resolve auto   # who's in it
agent-slack user get @alice @bob              # NDJSON default; @unresolved per miss; exit 0
agent-slack user dm-open @alice @bob          # group DM channel id
```

## Other domains

Each has full detail in `agent-slack <domain> usage` — read it only when the
task needs that domain (so finding a user never makes you load emoji, etc.):

| Domain | For | Detail |
|---|---|---|
| `usergroup` | subteams (`@group`): `list` / `get` / `members` | `agent-slack usergroup usage` |
| `emoji` | custom emoji: `list` / `get` / `search`; `add` / `remove` (`--yes`) | `agent-slack emoji usage` |
| `message draft` · `scheduled` | hand-off drafts for a human; scheduled sends | `agent-slack message usage` |
| `workflow` | discover and run Slack workflows | `agent-slack workflow usage` |
| `canvas` | fetch a canvas as Markdown | `agent-slack canvas usage` |
| `later` | saved-for-later (Slack's Later tab) | `agent-slack later usage` |
| `file` | point-pull a file (`F…`) seen in any output | `agent-slack file usage` |
| `cache` · `config` | inspect / warm / purge the cache; persist TTLs | `agent-slack cache usage` |
| `api` | raw Slack API escape hatch (`api call <method>`) | `agent-slack api usage` |

The resolution cache (channel/user/handle/workflow/emoji lookups) fills
automatically as you work and is transparent — reach for `cache` only to
pre-warm (`cache warm`) or clear it.

## More detail

- **Live, always-current:** `agent-slack usage` (overview) and
  `agent-slack <domain> usage` (per-domain — the authoritative source).
- **Bundled deep-dives:**
  [references/commands.md](references/commands.md) — full command map, split per
  domain, with flags and `--yes` gates ·
  [references/targets.md](references/targets.md) — targeting and multi-workspace ·
  [references/formatting.md](references/formatting.md) — Markdown, mentions, `--slack-markdown` ·
  [references/output.md](references/output.md) — NDJSON/meta contract, `--full`, payload shapes, cache.
