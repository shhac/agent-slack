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

## Credentials and auth import (implemented)

`internal/credential` and `internal/auth` are built.

`internal/credential` is the Store described under "Configuration and
credentials" below: creds.json metadata + macOS Keychain secrets behind a
`Keychain` interface (real `security` impl on darwin, no-op elsewhere, in-memory
for tests), with `__KEYCHAIN__` placeholders in the file. `--workspace`
selection (exact URL, else unique substring of url/host/name/team domain) lives
here as `Store.Resolve`.

`internal/auth` extracts browser credentials from local sources. Pure,
unit-tested cores: `ParseCurl`, `parseLocalConfig` (Chromium LevelDB value
decode), `parseTeamsJSON`, `decryptChromiumCookie` (PBKDF2-HMAC-SHA1 +
AES-128-CBC), Firefox `parseProfilesIni`. Platform orchestration:
`ExtractFromSlackDesktop` (pure-Go LevelDB read via `goleveldb` + Cookies
SQLite via `modernc.org/sqlite` + Safe Storage password from the Keychain),
`ExtractFromChrome`/`ExtractFromBrave` (osascript), `ExtractFromFirefox`
(profile SQLite). Both pure-Go deps preserve the single static binary.

The `auth` CLI commands (`internal/cli/auth.go`) are import-only:
`import-desktop`, `import-chrome`, `import-brave`, `import-firefox`,
`parse-curl`, plus `whoami`, `add`, `set-default`, `remove`. The store is
behind a `newStore` seam so CLI tests run against a temp file + in-memory
Keychain. (Windows Slack Desktop DPAPI cookie decryption is not yet ported.)

## Rendering (planned)

`internal/render` will hold the pure, well-tested conversion logic ported from
the TypeScript original (it is the highest-value, most-portable code):

- Slack mrkdwn → Markdown.
- `rich_text` / Block Kit blocks → a single Markdown string.
- Markdown / bullet lists → outbound `rich_text` blocks (for `send`/`edit`).
- Slack permalink parsing (`/archives/<channel>/p<ts>` + `thread_ts`).

## Configuration and credentials

- `internal/config`: non-secret workspace metadata (URL, team id, default
  workspace), stored at `~/.config/agent-slack/config.json` (XDG-aware).
- `internal/credential`: tokens/cookies stored in the macOS Keychain under
  service `app.paulie.agent-slack` (matching `lin`'s `app.paulie.lin`), with the
  per-workspace key as the account field. A local index records which workspace
  has which token kind. On non-macOS, falls back to a file with restrictive
  permissions (`keychain_darwin.go` / `keychain_other.go` build-tag split, as in
  the siblings). CLI commands report token presence as booleans or storage
  names, never values.

Credentials are **import-only** to start: there is no interactive setup dialog
(no `zenity` dependency). Tokens enter via `auth import-*` / `parse-curl` /
env vars and are written straight to the Keychain.

`auth import-desktop` reads Slack Desktop's Chromium *Local Storage* LevelDB to
recover `localConfig_v2`/`v3` (workspace `xoxc` tokens); the `xoxd` cookie comes
from Slack Desktop's separate cookie store. We read LevelDB with a pure-Go
reader (`github.com/syndtr/goleveldb/leveldb`) — no cgo, preserving the single
static binary — snapshotting to a temp dir first so a running Slack holding the
lock doesn't block us.

## Output

`internal/output`: format resolution (`json`/`yaml`/`jsonl`), pruning of null
fields, NDJSON list writer with `@pagination` meta lines, and the JSON error
contract.

## Mock server and tests

`cmd/mockslack` (planned) serves `internal/mockslack` — fixture-driven, not a
general Slack clone. Coverage combines: client unit tests (headers, 429 retry,
error mapping), render unit tests (the bulk of behavior parity with the TS
source), and CLI contract tests against `mockslack`.
