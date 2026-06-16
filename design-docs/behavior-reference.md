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

## Outbound formatting (send/edit)

- Escape `& < >`; promote `@U123` → `<@U123>` mentions.
- Detect bullet (`• - *`) and numbered (`1.`) lists → `rich_text_list` blocks.
- Plain markdown → `rich_text` structure (preserve mentions, emoji, channel
  refs, inline bold/italic/strike/code).

## File handling

- Prefer `url_private_download` over `url_private`.
- Canvas modes (`canvas`/`quip`/`docs`): download HTML → Markdown via a Go
  HTML→MD conversion.
- Infer extension from mimetype/filetype.
- On download failure, surface an `error` field rather than aborting the whole
  command.

## Rate limiting

- Browser path: retry 429 up to 3× with exponential backoff, cap ~30s.
- Standard path applies equivalent bounded retry and maps exhaustion to
  `fixable_by: retry`.

## Credentials

- Credentials live at `~/.config/app.paulie.agent-slack/credentials.json` with
  Keychain service `app.paulie.agent-slack` (family convention, per `lin`).
  Downloads and the user cache live separately under
  `~/.cache/app.paulie.agent-slack/` (see `architecture.md`).
- macOS Keychain stores tokens; the file stores a `"__KEYCHAIN__"` placeholder.
- The store schema is versioned (version, workspaces[], auth per workspace).
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
  field), `destinations` (`[{channel_id}]`), `file_ids` (required, may be `[]`),
  `is_from_composer`. A **scheduled** draft also sets `date_scheduled` (unix).
- `drafts.list` — returns every draft (filter on `date_scheduled`, `is_deleted`,
  `is_sent`); stored `file_ids` round-trip on read.
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
- `--resolve cached` expands every referenced user, channel, and usergroup into
  `referenced_users`/`referenced_channels`/`referenced_usergroups` maps from the
  per-workspace caches; `--resolve fresh` bypasses cached reads (users via
  users.info, channels via conversations.info, usergroups via a usergroups.list
  refetch). Unresolved ids are omitted. (search currently resolves users only.)

## Workflow and update behavior

- Workflow form-field submission is supported.
- There is no self-update command.
