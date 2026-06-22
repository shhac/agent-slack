# unreads · later · canvas · file · api commands

Global flags and the `--yes` gate convention: see the [commands index](../commands.md).
In-binary detail: `agent-slack <domain> usage` (e.g. `unreads usage`, `later usage`, `canvas usage`, `file usage`, `api usage`).

| Command | Key flags | Gate |
|---|---|---|
| `unreads` | `--counts-only`, `--max-messages` (10), `--max-body-chars` (4000), `--include-system`, `--slack-markdown`, `--format transcript` (`--tz`/`--with-ids`) | |
| `later list` | `--state`, `--limit` (20), `--max-body-chars` (4000), `--counts-only`, `--slack-markdown`, `--format transcript` (`--tz`/`--with-ids`) | |
| `later save\|complete\|archive\|reopen\|remove <target>` | `--ts` | |
| `later remind <target>` | `--in <30m\|2d\|tomorrow 9am>`, `--ts` | |
| `canvas get <canvas>` | `--max-chars` (20000), `--format transcript` (prints the Markdown body directly) | |
| `file download <file-id>` | `--workspace` | |
| `api call <method>` | `--params '<json>'\|<file>\|-`, `--multipart` | |

`unreads` / `later list` accept `--format transcript`: a human-readable digest
with a `──── summary ────` divider, then sections (channels for `unreads`,
states for `later`) of `[time] <name|id>` message blocks — the grouped sibling
of the `message get`/`list` conversation transcript.

`api call` is the raw escape hatch — POST any Slack Web API method with stored
credentials. Prefer the wrapped commands; reach for `api call` only when no
wrapper exists.
