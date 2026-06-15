# agent-slack

Slack CLI for AI agents — a token-efficient, structured-output tool for reading
and (carefully) writing Slack from an LLM agent. It belongs to the `agent-*` CLI
family (`agent-postmark`, `agent-stripe`, `lin`, …), sharing their conventions,
output contract, and credential handling.

The TypeScript [`agent-slack`](https://github.com/stablyai/agent-slack) was part
of the inspiration — it worked out a lot about driving Slack safely from an
agent — but this is its own tool with its own design.

> **Status:** feature-complete. The full command surface is implemented and
> tested against a fixture Slack server (`internal/mockslack`). See
> [`design-docs/`](design-docs/) for design decisions.

## Why Go

A single static binary with no runtime dependency, fast startup (matters for
per-call agent invocation), and alignment with the rest of the `agent-*` CLI
family so conventions, output contract, and credential handling are shared.

## Features

- **LLM-shaped output**: NDJSON lists, JSON single resources, aggressive pruning
  of empty fields, body truncation via `--max-body-chars`.
- **Structured errors**: every failure is JSON on stderr with `fixable_by`
  (`agent` | `human` | `retry`) and a `hint`.
- **Keychain-first credentials**: browser (`xoxc`/`xoxd`) and bot (`xoxb`/`xoxp`)
  tokens stored in the macOS Keychain; secrets never printed.
- **Mutation safety**: destructive commands (`message edit|delete`,
  `scheduled cancel`, `channel new|invite`) require `--yes` and describe what
  would happen without it.
- **Multi-workspace**: disambiguate with `--workspace <url-or-substring>`.

## Installation

### Homebrew

```bash
brew install shhac/tap/agent-slack       # upgrade: brew upgrade shhac/tap/agent-slack
```

### Go install

```bash
go install github.com/shhac/agent-slack/cmd/agent-slack@latest
```

Installs a binary stamped `dev`; for a version-stamped build, clone and
`make build`. Prebuilt binaries are also on the
[releases page](https://github.com/shhac/agent-slack/releases).

### Build from source

```bash
git clone https://github.com/shhac/agent-slack.git
cd agent-slack
make build
```

### Claude Code / AI agent skill

```bash
npx skills add shhac/agent-slack
```

This installs the `agent-slack` skill so Claude Code (and other AI agents) can
discover and drive `agent-slack` automatically. See
[skills.sh](https://skills.sh) for details.

## Getting started

```bash
agent-slack auth import-desktop            # from Slack Desktop (macOS/Linux/Windows)
agent-slack auth import-browser firefox    # or a browser: chrome|brave|firefox|zen|opera|safari
agent-slack auth test                      # verify credentials
agent-slack auth usage                     # every auth method (browser caveats, bot tokens, cURL, env vars)
agent-slack usage                          # LLM-oriented overview
agent-slack message usage                  # per-domain detail pages
```

## Command Surface

| Domain     | Commands |
|------------|----------|
| `auth`     | `list` (`ls`), `test`, `add`, `set-default`, `remove`, `import-desktop`, `import-browser <name>`, `parse-curl` |
| `message`  | `get`, `list`, `send`, `draft`, `edit`*, `delete`*, `react add/remove`, `scheduled list/cancel`* |
| `channel`  | `list`, `get`, `members`, `new`*, `invite`*, `mark` |
| `user`     | `list`, `get`, `dm-open` |
| `usergroup`| `list`, `get`, `members` (subteams; surfaces each group's default channels) |
| `search`   | `all`, `messages`, `files` |
| `workflow` | `list`, `preview`, `get`, `run` (incl. `--field` form submission) |
| `canvas`   | `get` |
| `unreads`  | (top-level) |
| `later`    | `list`, `save`, `complete`, `archive`, `reopen`, `remove`, `remind` |
| `file`     | `download` |
| `cache`    | `info`, `warm`, `purge` |
| `config`   | `get`, `set`, `list`, `unset` |
| `api`      | `call` (raw escape hatch) |

\* destructive — requires `--yes`, otherwise returns a description of what
would happen.

A fixture Slack server for manual testing ships as `cmd/mockslack`:

```bash
go run ./cmd/mockslack -addr 127.0.0.1:8765 -fixtures fixtures.json
agent-slack --base-url http://127.0.0.1:8765 auth test
```

## Development

```bash
make test        # go test ./... -count=1
make vet
make lint        # golangci-lint
make dev ARGS="usage"
```

## License

MIT — see [LICENSE](LICENSE).
