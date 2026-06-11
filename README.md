# agent-slack

Slack CLI for AI agents — a token-efficient, structured-output tool for reading
and (carefully) writing Slack from an LLM agent. This is a Go port of the
TypeScript [`agent-slack`](https://github.com/stablyai/agent-slack), carrying
over the learnings about how to build a Slack CLI that agents can drive safely.

> **Status:** scaffold. The command surface is being ported incrementally. See
> [`design-docs/initial-design.md`](design-docs/initial-design.md) for the plan
> and current progress.

## Why Go

The TypeScript original is ~22k LOC across 75 files and ships compiled binaries
anyway. A Go port gives a single static binary with no runtime dependency, fast
startup (matters for per-call agent invocation), and aligns with the rest of the
`agent-*` CLI family (`agent-postmark`, `agent-stripe`, `lin`, …) so conventions,
output contract, and credential handling are shared.

## Features (target)

- **LLM-shaped output**: NDJSON lists, JSON single resources, aggressive pruning
  of empty fields, body truncation via `--max-body-chars`.
- **Structured errors**: every failure is JSON on stderr with `fixable_by`
  (`agent` | `human` | `retry`) and a `hint`.
- **Keychain-first credentials**: browser (`xoxc`/`xoxd`) and bot (`xoxb`/`xoxp`)
  tokens stored in the macOS Keychain; secrets never printed.
- **Mutation safety**: state-changing commands require `--yes` as the
  human-in-the-loop gate.
- **Multi-workspace**: disambiguate with `--workspace <url-or-substring>`.

## Quick Start

```bash
make build
./agent-slack usage          # LLM-oriented overview of the command surface
./agent-slack --help
```

## Command Surface (planned)

| Domain     | Commands |
|------------|----------|
| `auth`     | `whoami`, `test`, `import-desktop`, `import-chrome`, `parse-curl` |
| `message`  | `get`, `list`, `send`, `edit`, `delete`, `react`, `scheduled` |
| `channel`  | `list`, `new`, `invite` |
| `user`     | `list`, `get` |
| `search`   | `all`, `messages`, `files` |
| `workflow` | `list`, `preview`, `get`, `run` |
| `canvas`   | `get` |
| `unreads`  | (top-level) |
| `later`    | `list`, `save`, `complete`, `archive`, `reopen`, `remove`, `remind` |

## Development

```bash
make test        # go test ./... -count=1
make vet
make lint        # golangci-lint
make dev ARGS="usage"
```

## License

MIT — see [LICENSE](LICENSE).
