# user commands

Global flags and the `--yes` gate convention: see the [commands index](../commands.md).
In-binary version: `agent-slack user usage`.

| Command | Notes |
|---|---|
| `user list` | `--limit` (200), `--cursor`, `--include-bots` |
| `user get <user…>` | accepts `U…`, `@handle`, or email; one → object, several → NDJSON (+ `{"@unresolved": […]}`) |
| `user dm-open <users…>` | returns the DM / group-DM channel id (up to 8 users) |
