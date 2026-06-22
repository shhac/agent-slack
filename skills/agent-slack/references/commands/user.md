# user commands

Global flags and the `--yes` gate convention: see the [commands index](../commands.md).
In-binary version: `agent-slack user usage`.

| Command | Notes |
|---|---|
| `user list` | `--limit` (200), `--cursor`, `--include-bots`, `--format transcript` |
| `user get <user…>` | accepts `U…`, `@handle`, or email; NDJSON default — one record or `{"@unresolved":{id,reason,fixable_by}}` per input in order; item-level miss → exit 0; `--format json` → object (one) or `{"data":[…],"@unresolved":[…]}` envelope (several); `--format transcript` for a human digest |
| `user dm-open <users…>` | returns the DM / group-DM channel id (up to 8 users) |

`user list`/`get` accept `--format transcript`: a human digest, one `@handle`
per line with name/title/tz and `🤖 bot`/`deactivated`/`DM open` tags (a
multi-target `get` adds a dim "Unresolved" section for misses).
