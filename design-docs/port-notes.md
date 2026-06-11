# Port notes: TS behaviors the Go port must preserve

Captured from the TypeScript `agent-slack` source so the port doesn't silently
drop hard-won behavior. Source: `../stablyai-agent-slack/src`.

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
- Canvas modes (`canvas`/`quip`/`docs`): download HTML → Markdown (turndown +
  GFM in TS; pick a Go HTML→MD path).
- Infer extension from mimetype/filetype.
- On download failure, surface an `error` field rather than aborting the whole
  command.

## Rate limiting

- Browser path: retry 429 up to 3× with exponential backoff, cap ~30s.
- Standard path relied on the SDK; the Go client should implement equivalent
  bounded retry and map exhaustion to `fixable_by: retry`.

## Credentials

- File: `~/.agent-slack/creds.json` in the TS version. The Go port uses the
  family convention (`~/.config/agent-slack/`); decide whether to read the old
  path for migration.
- macOS Keychain stores tokens; file stores `"__KEYCHAIN__"` placeholder.
  Keychain service is `app.paulie.agent-slack` (family convention, per `lin`).
- Zod-validated schema in TS (version, workspaces[], auth per workspace).
- **Import-only** to start: no interactive setup; tokens arrive via the
  `import-*` / `parse-curl` commands and env vars.

## auth import-desktop (LevelDB)

The TS path (`src/auth/desktop.ts`, `src/lib/leveldb-reader.ts`):

- Reads Slack Desktop's `Local Storage/leveldb` (Chromium Local Storage) to find
  `localConfig_v2` / `localConfig_v3` (or `reduxPersist:localConfig`), which
  hold the `teams` map with per-workspace `xoxc` tokens.
- The `xoxd` cookie comes from Slack Desktop's separate cookie store, not
  LevelDB.
- Snapshots the LevelDB dir to a temp location before reading, because a running
  Slack Desktop holds the DB lock.

Go port: use a pure-Go reader (`github.com/syndtr/goleveldb/leveldb`), no cgo.
The `chrome`/`brave`/`firefox` import paths instead read the same
`localConfig_v2/v3` from the browser's live `localStorage` via AppleScript /
profile parsing — unchanged in spirit.

## No draft editor

The TS `message draft` command spins up a localhost server + browser WYSIWYG
editor for a human to finish a message. This is dropped in the Go port: the tool
is LLM-first and an agent never drives a browser UI. Do not port the draft
server, its embedded HTML/JS, or the `message draft` command. Human-in-the-loop
is the `--yes` gate on destructive mutations (see `cli-design.md`).

## Deliberate divergences

Where the Go CLI intentionally differs from TS (NDJSON lists, compact
channel/user projections, download policy, no first-run browser
auto-extraction, `--yes` scope, `file download` / `api call` additions), the
record lives in `cli-design.md` — this file only tracks TS behaviors that must
be preserved.

## User resolution / caching

- In-memory user map; `--resolve-users` expands IDs to profiles,
  `--refresh-users` clears the cache first.

## Fork relationship

Personal fork `shhac/stablyai-agent-slack` carried these on top of upstream:
`fix-workflow-bookmark-id`, `remove-update-command`, `feat-workflow-fields`
(workflow form submission). Preserve workflow form-field submission and the
removal of the self-update command.
