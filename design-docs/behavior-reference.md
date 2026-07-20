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
- Legacy migration: a TypeScript agent-slack stored credentials at
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
