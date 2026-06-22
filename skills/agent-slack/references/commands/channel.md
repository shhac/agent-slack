# channel commands

Global flags and the `--yes` gate convention: see the [commands index](../commands.md).
In-binary version: `agent-slack channel usage`.

| Command | Key flags | Gate |
|---|---|---|
| `channel list` | `--user`, `--all`, `--limit` (100), `--cursor`, `--format transcript` | |
| `channel get <channel…>` | `--full`, `--format transcript` | |
| `channel members <channel>` | `--resolve none\|cached\|auto\|fresh`, `--limit` (100), `--cursor` | |
| `channel new` | `--name`, `--private` | `--yes` |
| `channel invite` | `--channel`, `--users`, `--external`, `--allow-external-user-invites` | `--yes` |
| `channel mark <target>` | `--ts` | |

`channel get <channel…>` accepts one or more ids/names and returns NDJSON by
default: one result per id in input order — the channel record, or
`{"@unresolved":{"id","reason","fixable_by"}}` for any that didn't resolve.
Item-level misses exit 0. `--format json` returns the pretty object (one id)
or `{"data":[…],"@unresolved":[…]}` envelope (several). `channel members` lists the user IDs in a channel
(chain into `user get`, or pass `--resolve cached`/`--resolve fresh` to expand to profiles inline).
`channel invite --users` accepts user IDs and (with `--external`) email
addresses, comma-separated. `channel mark` is personal read state, ungated.

`channel list`/`get` also take `--format transcript` — a human-readable digest:
a `──── Channels · N ────` divider then `#name`/`@dm` headlines with dim badges
(members, `🔒 private`, `🗄 archived`, `✓ member`) and the topic on a second line.
A multi-target `get` appends a dim "Unresolved" section for misses.
