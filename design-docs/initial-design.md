# agent-slack: initial design

Port of the TypeScript `agent-slack` (stablyai/agent-slack, plus Paul's fork
changes) to Go, adopting the `agent-*` CLI family conventions.

## Goals

1. Single static binary, fast cold start (agents invoke per-call).
2. Output and error contract identical to the rest of the `agent-*` family.
3. Behavior parity with the TS original for the read paths first, then writes.
4. Keychain-first secret handling; nothing sensitive in output.

## Auth model

Two token kinds, one interface:

- **Browser** (`auth_type: browser`): `xoxc-*` token + `xoxd-*` cookie, extracted
  from Slack Desktop or a browser. Calls go direct to
  `POST {workspace}/api/{method}` with the cookie header.
- **Standard** (`auth_type: standard`): `xoxb-*` / `xoxp-*` Bearer token via the
  official API host.

Resolution order per invocation: `--workspace` flag → env (`SLACK_TOKEN`,
`SLACK_COOKIE_D`, `SLACK_WORKSPACE_URL`) → stored default workspace. Secrets
resolve from Keychain; the config file holds only metadata + `__KEYCHAIN__`
placeholders.

Import paths to port (macOS-first; gate others clearly):
`auth import-desktop` (LevelDB), `auth import-chrome` / `import-brave`
(AppleScript), `auth import-firefox`, `auth parse-curl`. `auth list` (aliased
`whoami`) and `auth test` verify configuration.

## Command surface

Mirrors the TS CLI exactly so existing agent prompts/skills transfer:

- **auth**: `list` (`ls`, `whoami`), `test`, `add` (`--form`), `set-default`,
  `remove`, `import-desktop`, `import-chrome`, `import-brave`,
  `import-firefox`, `parse-curl`
- **message**: `get`, `list`, `send`, `edit`, `delete`,
  `react add|remove`, `scheduled list|cancel`
  (the TS `message draft` browser editor is intentionally dropped — see
  Decisions)
- **channel**: `list`, `new`, `invite`, `mark`
- **user**: `list`, `get`, `dm-open`
- **search**: `all`, `messages`, `files`
- **workflow**: `list`, `preview`, `get`, `run`
- **canvas**: `get`
- **unreads**: top-level
- **later**: `list`, `save`, `complete`, `archive`, `reopen`, `remove`, `remind`
- **file**: `download` (point pull of a file seen in any listing; new vs TS)
- **api**: `call` (raw Slack method escape hatch; new vs TS)

Flags, defaults, projections, and per-command details live in
`cli-design.md`.

### Targets

A `<target>` is a Slack permalink, `#channel` / `channel` / `C0123…`,
`@user` / `user` / `U0123…`, or a message `ts` (`1770165109.628379`). Permalink
parsing splits `p<digits>` into seconds + microseconds and reads `?thread_ts=`.

## Output contract

- Lists → NDJSON, trailing `{"@pagination": {...}}` line when more pages exist.
- Single resources → pretty JSON.
- `--max-body-chars` (default 8000; 4000 for search; `-1` = unlimited) truncates
  bodies with a `\n…` marker.
- `--full` restores normally-omitted bulky payloads; `--include-reactions` and
  `--resolve-users` opt into extra data.
- Errors → JSON on stderr: `{error, fixable_by, hint?}`.
  - `agent`: bad args/flags/targets.
  - `human`: auth, permissions, missing secrets.
  - `retry`: 429, 5xx, network.

## Safety

- Destructive mutations require `--yes`: `message edit|delete`,
  `message scheduled cancel`, `channel new|invite`. Without it they return a
  `fixable_by: human` error describing exactly what would happen. Plain
  `message send`, reactions, `workflow run`, and personal-state writes
  (`later *`, `channel mark`, `user dm-open`) are ungated by decision — see
  `cli-design.md` "Mutation gating".
- Browser path retries 429 with exponential backoff (cap ~30s).

## Port order

1. **Scaffold + contract** (this commit): root, output, errors, usage, CI, docs.
2. **Render package**: mrkdwn↔Markdown, blocks→Markdown, permalink parsing — pure
   functions, port the TS unit tests alongside.
3. **Slack client + mockslack**: DI transport, 429 retry, error mapping.
4. **Read commands**: `auth list/test`, `message get/list`, `channel list`,
   `user get/list`, `search`, `unreads`, `canvas get`.
5. **Write commands** (behind `--yes`): `message send/edit/delete/react`,
   `channel new/invite`, `workflow run`, `later` mutations, `scheduled`.
6. **Auth import** paths (LevelDB / browser extraction) — most platform-specific,
   do last.

## Decisions

- **No draft editor.** The TS `message draft` command opens a browser WYSIWYG
  editor for a human to finish and send. This tool is LLM-first and an agent
  will never drive a browser UI, so `message draft` is dropped entirely. The
  human-in-the-loop safeguard is the `--yes` requirement on mutations, not a
  draft step.
- **Pure-Go LevelDB reader, no cgo.** `auth import-desktop` reads Slack
  Desktop's Chromium *Local Storage* LevelDB to recover the `localConfig_v2`/`v3`
  JSON containing workspace `xoxc` tokens (the `xoxd` cookie comes from a
  separate cookies store). We read it with a pure-Go LevelDB reader
  (`github.com/syndtr/goleveldb/leveldb`) rather than shelling out or using a
  cgo binding — keeps the single-static-binary property. Snapshot the DB to a
  temp dir before reading so a running Slack Desktop holding the lock doesn't
  block us, matching the TS behavior.
- **Auth is import-only to start.** No interactive setup dialogs (`zenity` is
  not a dependency). Credentials arrive via the `import-*` / `parse-curl`
  commands and env vars; secrets are written straight to the Keychain.
- **Keychain naming follows the family.** Service name `app.paulie.agent-slack`
  (matching `lin`'s `app.paulie.lin`); the account field is the per-workspace
  key. macOS only via the `security` CLI; other platforms fall back to a
  restricted-permission file.
