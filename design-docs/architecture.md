# agent-slack architecture

This note records the intended package layout so the port stays inside clean
boundaries. It mirrors the conventions of the sibling `agent-*` CLIs
(`agent-postmark`, `agent-stripe`, `lin`).

## Command layer

`cmd/agent-slack` only calls `cli.Execute`. All CLI behavior lives in
`internal/cli`. Commands are registered by user-facing domain, one
`Register(parent)` per domain package or file:

- `root.go`: global flags (`--workspace`, `--format`, `--timeout`, `--debug`,
  `--full`), error formatting, command registration.
- `usage.go`: the LLM-optimized `usage` overview command.
- `auth.go`: credential import, `whoami`, `test`. Delegates secret storage to
  `internal/credential`.
- `message.go`, `channel.go`, `user.go`, `search.go`, `workflow.go`,
  `canvas.go`, `unreads.go`, `later.go`: resource commands. Shared list/mutation
  factories and compact projections live alongside.

Mutations return a structured `fixable_by: human` error unless `--yes` is set.

## Slack client

`internal/slack` is the HTTP client layer with dependency injection:

- `Doer` interface for tests and the `mockslack` server.
- `Sleep` hook so 429 retry/backoff is testable without real delays.
- Two transports behind one interface: the browser path (`xoxc` token + `xoxd`
  cookie, direct `POST {workspace}/api/{method}`) and the standard token path
  (`xoxb`/`xoxp` Bearer). 429s retry with exponential backoff capped at ~30s.
- Structured error mapping to `internal/errors.APIError` with `fixable_by`.

The client does not know about profiles, config files, redaction, or output
formatting — those stay in `internal/cli` and `internal/output`.

## Rendering

`internal/render` holds the pure, well-tested conversion logic ported from the
TypeScript original (it is the highest-value, most-portable code):

- Slack mrkdwn → Markdown.
- `rich_text` / Block Kit blocks → a single Markdown string.
- Markdown / bullet lists → outbound `rich_text` blocks (for `send`/`edit`).
- Slack permalink parsing (`/archives/<channel>/p<ts>` + `thread_ts`).

## Configuration and credentials

- `internal/config`: non-secret workspace metadata (URL, team id, default
  workspace), stored at `~/.config/agent-slack/config.json` (XDG-aware).
- `internal/credential`: tokens/cookies stored in the macOS Keychain, with a
  local index recording which workspace has which token kind. On non-macOS,
  falls back to a file with restrictive permissions. CLI commands report token
  presence as booleans or storage names, never values.

## Output

`internal/output`: format resolution (`json`/`yaml`/`jsonl`), pruning of null
fields, NDJSON list writer with `@pagination` meta lines, and the JSON error
contract.

## Mock server and tests

`cmd/mockslack` (planned) serves `internal/mockslack` — fixture-driven, not a
general Slack clone. Coverage combines: client unit tests (headers, 429 retry,
error mapping), render unit tests (the bulk of behavior parity with the TS
source), and CLI contract tests against `mockslack`.
