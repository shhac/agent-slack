# channel commands

Global flags and the `--yes` gate convention: see the [commands index](../commands.md).
In-binary version: `agent-slack channel usage`.

| Command | Key flags | Gate |
|---|---|---|
| `channel list` | `--user`, `--all`, `--limit` (100), `--cursor` | |
| `channel get <channel…>` | `--full` | |
| `channel members <channel>` | `--resolve none\|cached\|auto\|fresh`, `--limit` (100), `--cursor` | |
| `channel new` | `--name`, `--private` | `--yes` |
| `channel invite` | `--channel`, `--users`, `--external`, `--allow-external-user-invites` | `--yes` |
| `channel mark <target>` | `--ts` | |

`channel get` returns one channel's metadata (topic, membership, member count,
archive state; `--full` for the raw object). Pass several channels and it
returns NDJSON instead, one per line, with a trailing `{"@unresolved": […]}`
for any that didn't resolve. `channel members` lists the user IDs in a channel
(chain into `user get`, or pass `--resolve cached`/`--resolve fresh` to expand to profiles inline).
`channel invite --users` accepts user IDs and (with `--external`) email
addresses, comma-separated. `channel mark` is personal read state, ungated.
