# agent-slack

Slack CLI for AI agents. Go + cobra.

## Project Rules

- Lists default to NDJSON; single resources default to JSON.
- Errors are JSON on stderr with `fixable_by` (`agent` | `human` | `retry`) and a
  `hint`. Never exit with an unstructured error.
- Never print tokens or cookies. Secrets live in the macOS Keychain; the
  credentials file holds only non-secret metadata plus a `__KEYCHAIN__`
  placeholder.
- Prefer read-only commands. Destructive mutations (`message edit`/`delete`,
  `message scheduled cancel`, `channel new`/`invite`, `emoji add`/`remove`) must require `--yes` and
  return a human-fixable JSON error without it. Plain sends, reactions, and
  personal-state writes are ungated by decision — see
  `design-docs/cli-design.md`.
- Keep message bodies truncatable (`--max-body-chars`); omit bulky payloads from
  list output by default, restore with `--full`.
- Keep Slack HTTP logic dependency-injected so tests run without real network
  access; back CLI contract tests with a `mockslack` fixture server.

## Verification

```bash
GOCACHE=$(pwd)/.cache/go-build go test ./... -count=1
GOCACHE=$(pwd)/.cache/go-build go vet ./...
golangci-lint run ./...
```

## Keeping docs in sync

When you add or change commands, flags, output shapes, or notable behavior,
update all of these in the same change — they drift silently otherwise:

- **`internal/cli/usage_text.go`** — the top-level `usage` reference card
  (compiled into the binary; a change here ships in the next release).
- **`<domain> usage`** — the per-domain detail text for any affected domain.
- **`skills/agent-slack/SKILL.md`** and **`skills/agent-slack/references/`** —
  the LLM-facing skill. SKILL.md is the always-loaded entry (contract + common
  paths + a domain router); per-domain command detail lives in
  `references/commands/<domain>.md` (indexed by `references/commands.md`), and
  the cross-cutting contracts in `output.md`/`targets.md`/`formatting.md`. A
  command/flag change touches the matching `commands/<domain>.md`; keep the
  stderr/output contract in step with `usage`.
- **`design-docs/`** — record the design decision, not just the code. New
  behavior or a contract change belongs in `cli-design.md`,
  `behavior-reference.md`, or `architecture.md` as appropriate.
- **`README.md`** — when the change is user-facing.

## References

The full design and command surface live in `design-docs/`:

- `initial-design.md` — command surface, auth model, output contract.
- `architecture.md` — package layout and boundaries.
- `behavior-reference.md` — Slack API handling the implementation relies on.
