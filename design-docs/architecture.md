# agent-slack architecture

This note records the intended package layout so the port stays inside clean
boundaries. It mirrors the conventions of the sibling `agent-*` CLIs
(`agent-postmark`, `agent-stripe`, `lin`).

## Command layer (implemented)

`cmd/agent-slack` only calls `cli.Execute`. All CLI behavior lives in
`internal/cli`. Commands are registered by user-facing domain, one
`Register(parent)` per domain package or file:

- `root.go`: global flags (`--workspace`, `--format`, `--timeout`, `--debug`,
  `--full`), error formatting, command registration.
- `usage.go`: the LLM-optimized `usage` overview command, plus per-domain
  `<domain> usage` pages as command families land (see `cli-design.md`).
- `auth.go`: credential import, `whoami`, `test`. Delegates secret storage to
  `internal/credential`.
- `message.go`, `channel.go`, `user.go`, `search.go`, `workflow.go`,
  `canvas.go`, `unreads.go`, `later.go`, `file.go`, `api.go`: resource
  commands. Shared list/mutation factories and compact projections live
  alongside.

Destructive mutations (`message edit|delete`, `scheduled cancel`,
`channel new|invite`) return a structured `fixable_by: human` error unless
`--yes` is set; other writes are ungated. The full command tree, flag
defaults, projections, and the decisions behind them are in `cli-design.md`.

## Slack client (implemented)

`internal/slack` is the HTTP client layer with dependency injection:

- `Doer` interface and a sleep hook so 429 retry/backoff tests run without
  real network or delays.
- Two transports behind one `Client`: the browser path (`xoxc` token in the
  form body + `xoxd` cookie, direct `POST {workspace}/api/{method}`) and the
  standard token path (`xoxb`/`xoxp` Bearer against `https://slack.com`,
  overridable base URL). `APIMultipart` covers internal methods (`saved.*`)
  that ignore urlencoded params.
- 429s retry up to 3× honouring `Retry-After`, clamped to [1s, 30s].
- Structured error mapping to `internal/errors.APIError` with `fixable_by`;
  `ErrorCode`/`IsAuthError` expose the Slack code without string matching,
  and `response_metadata.messages` details become hints.
- `WithAuthRefresh` is the auto-refresh seam: on an auth error the hook is
  consulted once per client for replacement credentials and the call retried.
  The CLI wires it to Slack Desktop re-extraction.
- Resolvers: `ResolveChannelID` (the `search.messages in:#name` one-call
  trick, pagination fallback), `ResolveChannelName`, `ResolveUserID`
  (ID/email/@handle), plus `EachPage` cursor pagination.
- `ResolveUsersByID`: per-workspace 24h disk cache of compact user profiles
  (the CLI cold-starts per agent call, so the cache must persist on disk).

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

## Rendering (implemented)

`internal/render` holds the pure conversion logic ported from the TypeScript
original — no network, no I/O, table-tested for behavior parity with the TS
unit tests:

- Slack permalink parsing (`/archives/<channel>/p<ts>` + `thread_ts`, with the
  truncated-URL heuristic) and CLI `<target>` parsing (`url.go`, `target.go`).
- Slack mrkdwn → Markdown, with :emoji: → unicode via a data table generated
  from the same emojilib dataset node-emoji uses (`mrkdwn.go`, `emoji.go`,
  `emoji_data.go`).
- Message → one Markdown string in priority order rich_text → Block Kit →
  legacy text+attachments, including forwarded-message handling
  (`message.go`, `richtext_mrkdwn.go`).
- Outbound: text → `rich_text` blocks (list/code/quote detection, inline
  parsing) and mrkdwn escaping/mention promotion (`richtext.go`,
  `outbound.go`). The TS inline regex used lookarounds RE2 lacks; the Go
  scanner was differentially fuzzed against the TS implementation.
- Raw message JSON → `MessageSummary` / `CompactMessage` shaping
  (`compact.go`). API-dependent pieces (user resolution, file downloads,
  snippet enrichment via `files.info`) stay in the client layer, which fills
  `DownloadedPaths` and `FileSnippet`.

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

## Mock server and tests (implemented)

`internal/mockslack` is a fixture-driven fake of the Web API (not a Slack
clone): per-method response queues with the last response sticky, recorded
calls for assertions, and an `ExpectToken` knob that answers `invalid_auth`
(without consuming a fixture) to exercise auth and refresh paths.
`cmd/mockslack` serves it standalone from a fixtures JSON file for manual
testing and CLI contract tests. Coverage combines: client unit tests
(headers, 429 retry, error mapping, refresh), render unit tests (the bulk of
behavior parity with the TS source), and CLI contract tests against
`mockslack`.
