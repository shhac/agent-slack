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
- Zod-validated schema in TS (version, workspaces[], auth per workspace).

## User resolution / caching

- In-memory user map; `--resolve-users` expands IDs to profiles,
  `--refresh-users` clears the cache first.

## Fork relationship

Personal fork `shhac/stablyai-agent-slack` carried these on top of upstream:
`fix-workflow-bookmark-id`, `remove-update-command`, `feat-workflow-fields`
(workflow form submission). Preserve workflow form-field submission and the
removal of the self-update command.
