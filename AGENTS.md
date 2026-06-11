# agent-slack

Slack CLI for AI agents. Go + cobra. Port of the TypeScript `agent-slack`.

## Project Rules

- Lists default to NDJSON; single resources default to JSON.
- Errors are JSON on stderr with `fixable_by` (`agent` | `human` | `retry`) and a
  `hint`. Never exit with an unstructured error.
- Never print tokens or cookies. Secrets live in the macOS Keychain; the
  credentials file holds only non-secret metadata plus a `__KEYCHAIN__`
  placeholder.
- Prefer read-only commands. Any command that changes Slack state (`message
  send`/`edit`/`delete`, `channel invite`, `workflow run`) must require `--yes`
  and return a human-fixable JSON error without it.
- Prefer `message draft` to put a human in the loop before sending.
- Keep message bodies truncatable (`--max-body-chars`); omit bulky payloads from
  list output by default, restore with `--full`.
- Keep Slack HTTP logic dependency-injected so tests run without real network
  access; back CLI contract tests with a `mockslack` fixture server.

## Verification

```bash
GOCACHE=$(pwd)/.cache/go-build go test ./... -count=1
GOCACHE=$(pwd)/.cache/go-build go vet ./...
```

## References

The full porting plan and command surface live in `design-docs/`:

- `initial-design.md` — command surface, auth model, output contract.
- `architecture.md` — package layout and boundaries.
- `port-notes.md` — TypeScript-source behaviors the Go port must preserve.
