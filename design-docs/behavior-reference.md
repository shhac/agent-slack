# Behavior reference: Slack API handling agent-slack relies on

The Slack-side behaviors, parsing rules, and algorithms the implementation
depends on. Keep this current as the handling evolves.

## Slack permalink / target parsing

- Format: `https://{workspace}/archives/{channel}/p{ts_no_decimal}[?thread_ts=…]`.
- `p(\d{6,})(\d{6})` splits the trailing 6 digits as microseconds, the rest as
  seconds → `seconds.microseconds`.
- Workspace URL normalizes to `https://{host}` (drop any path).
- `thread_ts` from the query is a hint used to scan a thread when the message is
  not in channel history.

## Thread handling

- `conversations.history` does not guarantee thread replies; fall back to
  `conversations.replies` keyed on the root `ts`.
- Root `ts == thread_ts`; replies share `thread_ts` but have distinct `ts`.

## Message rendering (priority order)

1. `rich_text` blocks (modern).
2. Block Kit `blocks`.
3. legacy `text` + `attachments`.

All collapse to one Markdown string. Forwarded content: extract
`message_blocks` from attachments; parse `forwarded_threads` from URLs.

### Work Object unfurls (app cards)

App link unfurls (issue trackers etc.) arrive as attachments carrying **only**
`{from_url, id, work_object_entity}` — no classic fields (`text`, `title`,
`fallback`), no blocks, empty top-level `text` — so without a dedicated path
the whole message renders empty. Slack documents only the write side
(`chat.unfurl` entity payloads); the read-back shape below is
reverse-engineered from live payloads:

- `work_object_entity.external_url` — the entity link; `display_type`,
  `app_name`/`product_name` describe it.
- `work_object_entity.layouts.{compact,expanded}` — `title.text`,
  `subtitle.text` (e.g. "Issue EX-123 in TrackerApp"), plus app chrome we skip
  (`header_title`, `hover_subtitle`). The `expanded` layout adds
  `fields.elements[]`: `{label, rich_text}` where `rich_text` is a standard
  rich_text block (status, assignee as `user` elements, links).

Rendered as title(+link), subtitle, then `Label: value` field lines — a
first-class content source in the normal-attachment chunk (before the
`fallback` last-resort), so classic fields and work objects compose if an app
ever sends both.

## Outbound formatting (send/edit)

- Escape `& < >`; promote `@U123` → `<@U123>` mentions.
- Detect bullet (`• - *`) and numbered (`1.`) lists → `rich_text_list` blocks.
  Slack has **no nested-list container**: a sub-list is a sibling block carrying
  `indent` (depth), and `offset` restarts an ordered block's count at `offset+1`.
  So a numbered list interrupted by a sub-list is three sibling blocks, and only
  `offset` keeps 1/2/3 from becoming 1/1/1. Both fields are stored verbatim
  (verified against live Slack), so the conversion is the only thing that has to
  track depth and carry the running count across blocks.
- Plain markdown → `rich_text` structure (preserve mentions, emoji, channel
  refs, inline bold/italic/strike/code).
- Upgrade unlabeled links to the chips Slack's composer makes: a same-workspace
  message permalink → a `message_mention` element; any other unlabeled web URL
  (`[url](url)`/`<url>`) → a `link` with a scheme-stripped label + `truncated:true`.
  Labeled links are left as-is. See `cli-design.md` "Inline link chips".

## File handling

- Prefer `url_private_download` over `url_private`.
- Canvas modes (`canvas`/`quip`/`docs`): download HTML → Markdown via a Go
  HTML→MD conversion.
- Infer extension from mimetype/filetype.
- On download failure, surface an `error` field rather than aborting the whole
  command.

## Rate limiting

- Browser path: retry 429 up to 3× honouring `Retry-After`, cap 60s.
- Standard path applies equivalent bounded retry and maps exhaustion to
  `fixable_by: retry`.
- Every 429 emits a structured notice on stderr (`{"notice": ...}`); the
  terminal hit adds a hint about Slack's 1 req/min non-Marketplace tier on
  `conversations.history`/`.replies`.

## Credentials

- Credentials live at `~/.config/app.paulie.agent-slack/credentials.json` with
  Keychain service `app.paulie.agent-slack` (family convention, per `lin`).
  Downloads and the user cache live separately under
  `~/.cache/app.paulie.agent-slack/` (see `architecture.md`).
- macOS Keychain stores tokens; the file stores a `"__KEYCHAIN__"` placeholder.
- The store schema is versioned (version, workspaces[], auth per workspace).
  Each workspace also carries non-secret `team_id`/`user_id` (resolved from
  `auth.test`, backfilled lazily) that key the per-identity cache namespace —
  see `cache-namespacing.md`.
- **Import-only** to start: no interactive setup; tokens arrive via the
  `import-*` / `parse-curl` commands and env vars.
- Legacy migration: another Slack CLI stores credentials at
  `~/.config/agent-slack/credentials.json`; that file seeds a missing store once,
  read-only.

## auth import-desktop (LevelDB)

- Reads Slack Desktop's `Local Storage/leveldb` (Chromium Local Storage) to find
  `localConfig_v2` / `localConfig_v3` (or `reduxPersist:localConfig`), which
  hold the `teams` map with per-workspace `xoxc` tokens.
- The `xoxd` cookie comes from Slack Desktop's separate cookie store, not
  LevelDB.
- Snapshots the LevelDB dir to a temp location before reading, because a running
  Slack Desktop holds the DB lock.
- Uses a pure-Go LevelDB reader (`github.com/syndtr/goleveldb/leveldb`), no cgo.

The `chrome`/`brave`/`firefox` import paths instead read the same
`localConfig_v2/v3` from the browser's live `localStorage` via AppleScript /
profile parsing.

## Drafts and scheduled messages (`drafts.*`, client API)

Drafts are a **client-only** concept: `chat.scheduleMessage` and
`chat.scheduledMessages.list` reject browser (`xoxc`) tokens with
`not_allowed_token_type`, so on browser auth the desktop client stores a
scheduled message as a **scheduled draft** via the `drafts.*` methods. We do the
same. (No browser draft *editor* — LLM-first; the draft is a data hand-off, not
a UI.)

Methods (all accept `xoxc`):

- `drafts.create` — params: `client_msg_id` (UUID — a non-UUID fails with
  `invalid_client_msg_id`), `blocks` (rich_text — a draft has no plain-text
  field), `destinations` (`[{channel_id, thread_ts?}]`), `file_ids` (required,
  may be `[]`), `is_from_composer`. A **scheduled** draft also sets
  `date_scheduled` (unix). A `thread_ts` inside the destination makes the draft a
  thread reply — verified live: `drafts.create` echoes it back (and fills in
  `broadcast`/`user_ids`), `drafts.list` returns it, and sending the draft
  (`chat.postMessage`/`files.share` with `thread_ts`) posts the reply in-thread.
  This is how Slack itself models a draft started in a thread, so the draft lives
  in the thread across review and through a `--schedule*` promotion.
- `drafts.list` — returns every draft (filter on `date_scheduled`, `is_deleted`,
  `is_sent`); stored `file_ids` and the destination `thread_ts` round-trip on read.
- `drafts.info` — single draft by `draft_id`.
- `drafts.update` — edit; same fields as create plus `client_last_updated_ts`.
- `drafts.delete` — soft-delete (sets `is_deleted`); needs `client_last_updated_ts`.

`client_last_updated_ts` is the client's **current wall-clock** at edit time
(last-writer-wins) — a fresh "now" value wins; the draft's stored
`last_updated_ts` is *not* what the server compares against.

**`is_from_composer` is load-bearing (verified live).** It controls two
independent things:

1. *The compose box.* An `is_from_composer: false` draft pre-fills the channel's
   message input when the input is empty (it backs the input); an
   `is_from_composer: true` draft never touches the input. Both are findable in
   the client's Drafts list.
2. *Dedup.* Slack allows at most **one** `is_from_composer: false` draft per
   target (a second `drafts.create` fails with `attached_draft_exists`) but
   **many** `is_from_composer: true` drafts per target.

We create every hand-off draft as **`is_from_composer: true`**: it never shoves
our text into the user's input box (no accidental send), and many-per-target
means concurrent agents don't collide on a single slot. The cost: our drafts are
then indistinguishable from drafts the user started in-app (no "source" field
exists) — both appear in `drafts.list` — so the CLI addresses a draft by its id
(`Dr…`), treating a target as a convenience only when it resolves to exactly one
draft (otherwise it errors and lists the candidate ids). Draft kinds, by
(`is_from_composer`, `date_scheduled`):

- ours / the user's in-app drafts — `true`, `0` (many per target, id-addressed)
- scheduled messages — `true`, `>0` (many per target, id-addressed)
- a *detached* draft — `false`, `0` (one per target; we never create these, and
  they can't be scheduled — `scheduled_draft_cannot_be_attached`)

**Attaching a file to a draft (verified).** A draft references a file by id, but
`drafts.create` rejects a *pending* upload with `file_not_found` — the file must
be finalized first. Upload the bytes (`files.getUploadURLExternal` → POST), then
`files.completeUploadExternal` with the file but **no `channel_id`**: that turns
the pending upload into a real file *without posting it*, and `drafts.create`
then accepts the id. (Same no-channel `files.completeUpload` step the web client
uses.) Uploads run in parallel; the completion finalizes them.

**Sending a draft.** There is no `drafts.send`. A draft that carries files goes
via `files.share` (`draft_id` + comma-joined `files` + `blocks`) — the native
"send message with files" path, which posts and removes the draft in one call
(`chat.postMessage` can't re-attach an already-uploaded file). A fileless draft
posts via `chat.postMessage` carrying `draft_id`, so Slack removes the draft as
part of the post — no separate, raceable `drafts.delete`.

**Promotion (draft → scheduled).** A single `drafts.update` that adds
`date_scheduled` flips a draft to a scheduled message in place (verified): same
`draft_id`, it moves from the plain `list` to the scheduled `list`, re-sending
`file_ids` so attachments survive to delivery. This backs
`message draft send --schedule/--schedule-in`.

**Completion cache.** `drafts.list` write-warms a "drafts" completion category
(ids + text) so the shell can suggest draft ids. Like the scheduled-id cache, it
is *not* part of `cache warm` (which sweeps stable resolution data —
users/channels/usergroups), and stale ids (sent, deleted, or promoted) age out
at the category TTL rather than being actively evicted: a completion that offers
a gone id simply errors gracefully when used.

Human-in-the-loop is the `--yes` gate on destructive mutations (see
`cli-design.md`).

## Deliberate divergences

The broader behavior and output decisions (NDJSON lists, compact channel/user
projections, download policy, no first-run browser auto-extraction, `--yes`
scope, `file download` / `api call` additions) are recorded in `cli-design.md`.

## Emphasis has two dialects, and they disagree

Outbound we read **CommonMark**: `*` may open mid-word (`foo**bar**baz` bolds
`bar`), `_` may not (so `snake_case_word` survives). Inbound we read **Slack
mrkdwn**, whose parser will not open a delimiter that follows a word character
— so `2*3 and 4*5`, `src/*.go`, and `a~b` are literal text on screen.

Applying the outbound rule inbound invented emphasis Slack never displayed,
across arithmetic, globs, paths, and tilde-bearing identifiers; it also turned
`**hi**` into `***hi***`. RE2 has no lookaround, so the inbound patterns capture
both boundaries and the replacement repeats until the text settles — a match
consumes the trailing boundary that the next adjacent run needs as its leading
one.

Two consequences worth stating:

- **An escaped marker forces the rich_text path.** Stripping the backslash is
  only half the job: the result travels in the `text` field, which Slack parses
  as mrkdwn, so a bare `*literal*` arrives **bold**. The escape is honoured
  here and undone on arrival unless the characters ride as inert rich_text.
- **A styled run crossing a newline is wrapped per line.** Slack emits one
  element for a bold run spanning a line break; wrapping it whole yields
  `*a\nb*`, which the inbound converter refuses (delimiters do not span lines)
  — losing the emphasis and injecting two literal asterisks.

## User IDs on Enterprise Grid

Slack issues both `U…` and `W…` user IDs; `W…` belongs to Enterprise Grid and
Slack Connect users. One rule (`render.IsUserID`) covers both, everywhere.

They diverged once, and the failures were silent rather than loud: mention
rendering accepted `W…` while target parsing did not, so a `W…` target fell
through to *channel-name* resolution (`#W01ENTERPRISE`), `ResolveUserID` treated
it as a handle, and a `W…` reactor was filtered out of `reactions[].users`
leaving only a count that no longer matched.

Worth recording because it is an easy assumption to make: a separate Slack CLI
(stablyai/agent-slack) carried the same `^U[A-Z0-9]{8,}$` rule and corrected it
in July 2026. Two implementations arrived at the same wrong shape
independently, which is a good reason to treat every id predicate here as
suspect until it has been checked against a real Enterprise Grid id.

## Referenced-entity resolution / caching

- A rich_text mention carries only the bare id (`{user_id}`/`{channel_id}`/
  `{usergroup_id}`, no label — verified), so making `<@U…>`/`<#C…>`/`<!subteam^S…>`
  mentions legible means resolving each id.
- `--resolve` expands every referenced user, channel, and usergroup into
  `referenced_users`/`referenced_channels`/`referenced_usergroups` maps. Modes:
  `none` (off), `cached` (cache-only, never fetch), `auto` (cache then fetch
  misses unless the category's completeness sentinel is fresh — then a miss is
  authoritative and skipped; prints a stderr `cache warm` hint when it fetched),
  `fresh` (bypass cached reads). **`auto` is the default for message get/list and
  search**; `members` lists default to `none` (bulk expansion stays opt-in).
  Fetches: users via users.info, channels via conversations.info (per id),
  usergroups via one usergroups.list. Unresolved ids are omitted. `search`
  resolves all three too (it maps --resolve to cache-then-fetch / bypass, so its
  `cached` is effectively cache-then-fetch — the cache-only nuance is get/list-only).

## Workflow and update behavior

- Workflow form-field submission is supported.
- Form submission follows the real client's sequence (verified against a
  captured browser session): `workflows.triggers.trip` → wait for the
  `view_opened`/`view_push` RTM event → `views.get` for the authoritative
  view (the push payload can be a stub when several clients share the
  session; the fetch is best-effort with the event view as fallback) →
  `views.submit`. There is **no** finalization call after `views.submit` —
  its response is the final word.
- `views.submit` success returns `{"ok":true,"view":null,"response_action":
  "clear"}`. Validation failures are **also** `ok:true`, with
  `response_action: "errors"` plus a block_id-keyed `errors` map (the Block
  Kit modal contract). The CLI maps those to real errors; bare `ok` is never
  treated as success. Block ids are mapped back to field titles best-effort.
- Form state entries must mirror each rendered element's type: the builder's
  "Rich text composer" (and long/paragraph fields) render as
  `rich_text_input` and expect a `rich_text_value` document, not a
  `plain_text_input` value; selects/radio/checkboxes expect the element's
  option object(s) copied verbatim (`selected_option(s)`, full `text` object
  included); datepicker expects `selected_date` (`YYYY-MM-DD`). Mismatched
  shapes are rejected only via `response_action: "errors"`.
- Workflow form views set `notify_on_close: true`, so `views.close` cancels
  the tripped run. The CLI uses this deliberately: when submission is
  abandoned after tripping (unsupported field type, unmatched option,
  Slack-side rejection), it closes the view rather than leaving a dangling
  modal on the user's other clients.
- The real client mints a fresh `web-<millis>` `client_token` per call (trip
  and submit tokens differ within one submission) — tokens do not correlate
  the flow, so the CLI's per-call `cli-<millis>` tokens are equivalent.
- `--debug` logs every received RTM frame (token-redacted, truncated), which
  is the only visibility into the push events driving this flow.
- There is no self-update command.

## The event socket (`client.getWebSocketURL`)

The web client does not poll for new messages: it renders its message pane from
a long-lived WebSocket, and after a `chat.postMessage` it makes no history call
at all. This is the transport a `message await` / `message stream` would be
built on. Verified against a captured browser session.

- **Slack's push transports are unavailable to us.** The Events API needs a
  publicly reachable request URL and an installed app; Socket Mode needs an
  app-level `xapp-` token with `connections:write` plus a bot token. Both are
  app artifacts that cannot be derived from a browser session, and both would
  give bot visibility rather than the user's own view. The event socket is the
  browser-auth equivalent.
- **The modern client does not call `rtm.connect`.** It calls
  `client.getWebSocketURL`, which returns `primary_websocket_url`,
  `fallback_websocket_url`, `routing_context`, and `ttl_seconds: 604800` — a
  week, so the endpoint is worth caching rather than re-fetching per connect.
  (`rtm.connect` still works on `xoxc` and still backs the workflow form flow;
  its URL is short-lived and single-use.)
- The client assembles the connect URL itself, appending `token`,
  `sync_desync=1`, `slack_client=desktop`, `start_args`, `flannel=3`,
  `lazy_channels=1`, `no_query_on_subscribe=1`, `batch_presence_aware=1`, and
  `gateway_server=<routing_context>`. `start_args` carries `connect_only=true`,
  which suppresses the boot payload — events only.
- **No subscription is required.** `lazy_channels` / `no_query_on_subscribe`
  suggest the server expects the client to declare interest per conversation,
  but a 15-minute capture that sent nothing except keepalive pings received
  messages, edits, bot posts, reactions and typing across both channels and
  DMs it had never named. A consumer can connect and listen.
- The socket survives at least 15 minutes on a 30s client ping (each answered
  with `pong`). `reconnect_url` is pushed periodically — a pre-authorized URL
  to reconnect with, so a long-lived consumer need not re-fetch
  `client.getWebSocketURL`.
- Socket `message` frames carry `blocks` (rich_text) alongside `text`, and —
  unlike the Events API's payloads — **no `channel_type`**. Conversation kind
  has to come from the id prefix.

Three shapes will silently corrupt a naive consumer, and
`mockslack.DefaultEventScript` models each:

- **A thread reply arrives as two frames.** The reply itself (`message` with
  `thread_ts`), and then `message_replied` — the *parent* message re-sent with
  updated `reply_count` / `latest_reply` / `reply_users`. Treating every
  message-typed frame as new activity re-emits the parent as though it had
  just been posted.
- **A bot message has no `user` field.** `subtype: bot_message` carries
  `bot_id` / `username` / `bot_profile` / `app_id` instead. Keying on `user`
  drops app output entirely — which is most of what an agent waits on.
- **Edits and deletes are message-typed with `hidden: true`** and no text of
  their own (`message_changed` nests `message` + `previous_message`;
  `message_deleted` carries only `deleted_ts`).

The rest is bookkeeping from the user's other clients that looks like activity
but carries none — `im_marked`, `badge_counts_updated`,
`clear_mention_notification`, `update_global_thread_state`, `thread_subscribed`,
`user_invalidated`, `dnd_invalidated`, `activity/activity_updated`,
`activity/activity_deleted`, `file_view_ready` — plus `desktop_notification`,
which duplicates content the message frame already delivered (and puts the
message's ts in `msg`, not `ts`). Over 15 minutes these outnumbered actual
messages roughly 15:1, so a stream must filter rather than forward.
- The client's own incremental fetch is `conversations.history` with `oldest`,
  `inclusive=true`, `ignore_replies=true` and an `_x_reason` of
  `message-pane/requestHistory` — i.e. the cursor pattern a polling fallback
  would use, on the workspace client endpoint, where the 1 req/min
  non-Marketplace tier does not apply.
- **Your own DM publishes no socket events at all.** Verified from both ends:
  messages sent to it from the Slack client and via `chat.postMessage`, plus a
  reaction, an edit, and a thread reply, produced zero frames — while a passive
  capture over the same window carried traffic from other conversations. The
  same API send to a real DM comes straight back on the socket, so this is the
  conversation, not the send path. Presumably Slack has nobody to notify and
  the client renders "note to self" locally. `message await`/`stream` therefore
  need `--poll` for that one conversation.
- The `client.getWebSocketURL` response carries a **fallback gateway** as well
  as the primary. It is dialed when the primary refuses, so one gateway's
  outage does not end a run.
- Reaction names arrive with the reactor's skin tone attached
  (`+1::skin-tone-3`). One normalizer (`render.NormalizeReactionName`) serves
  every command that takes an emoji, so `message react` and
  `message await --reaction` accept the same inputs — including unicode.
- Two methods worth using for a stream that are not wrapped yet:
  `client.counts` (per-conversation latest ts + unread state in one call — the
  cheap way to find *which* conversation moved) and `messages.list`
  (`message_ids: [{channel, timestamps[]}]` — batch fetch of specific messages
  across channels, for reconnect gap-fill).
