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

- `drafts.create` — params: `client_msg_id` (UUID), `blocks` (rich_text — a
  draft has no plain-text field), `destinations` (`[{channel_id}]`), `file_ids`
  (required, may be `[]`), `is_from_composer`. A **scheduled** draft adds
  `date_scheduled` (unix) and must set `is_from_composer: true`; a **plain**
  draft sets `is_from_composer: false`.
- `drafts.list` — returns every draft (filter on `date_scheduled`, `is_deleted`,
  `is_sent`).
- `drafts.info` — single draft by `draft_id`.
- `drafts.update` — edit; same fields as create plus `client_last_updated_ts`.
- `drafts.delete` — soft-delete (sets `is_deleted`); needs `client_last_updated_ts`.

`client_last_updated_ts` is the client's **current wall-clock** at edit time
(last-writer-wins) — a fresh "now" value wins; the draft's stored
`last_updated_ts` is *not* what the server compares against.

Cardinality (verified against the API):

- **Plain (unscheduled) drafts: at most one per target.** A second
  `drafts.create` to a target that already has a plain draft fails with
  `attached_draft_exists`. So a plain draft is target-addressed.
- **Scheduled drafts: many per target.** Multiple `date_scheduled` drafts to the
  same channel coexist, so scheduled messages are id-addressed.

There is no `drafts.send`: "send a draft now" composes `chat.postMessage`
(browser-allowed) with the draft's blocks, then `drafts.delete`.

**Promotion (plain draft → scheduled).** A single `drafts.update` that adds
`date_scheduled` + `is_from_composer: true` flips a plain draft to a scheduled
message in place (verified): same `draft_id`, it moves from the plain `list` to
the scheduled `list`, with no separate post/delete. This backs
`message draft send --schedule/--schedule-in`. (The reverse — a *detached*
draft, `is_from_composer: false`, cannot be scheduled: `scheduled_draft_cannot_be_attached`.)

Human-in-the-loop is the `--yes` gate on destructive mutations (see
`cli-design.md`).

## Deliberate divergences

The broader behavior and output decisions (NDJSON lists, compact channel/user
projections, download policy, no first-run browser auto-extraction, `--yes`
scope, `file download` / `api call` additions) are recorded in `cli-design.md`.

## User resolution / caching

- In-memory user map; `--resolve-users` expands IDs to profiles,
  `--refresh-users` clears the cache first.

## Workflow and update behavior

- Workflow form-field submission is supported.
- There is no self-update command.
