# usergroup commands

Global flags and the `--yes` gate convention: see the [commands index](../commands.md).
In-binary version: `agent-slack usergroup usage`.

Workspace user groups (subteams, the `@group` you @-mention). Aliased `usergroups`.

| Command | Notes |
|---|---|
| `usergroup list` | `--include-disabled`, `--limit` (200, max 1000), `--cursor`; compact rows: `id` (S…), `handle`, `name`, `description`, `user_count`, and `channels`/`groups` (the group's **default** channels/subteams — members are auto-added); paginated via `{"@pagination":{next_cursor}}` |
| `usergroup get <usergroup…>` | accepts `S…` or `@handle`; one → object, several → NDJSON (+ `{"@unresolved": […]}`) |
| `usergroup members <usergroup>` | user ids by default; `--resolve cached`/`fresh` for profiles, `--include-disabled` |

`channels` lists **all** the group's default channels — the CLI takes no view on
which is "best" to post in; choose per your use case. To answer "which groups am
I in?", check your user id (from `auth test`) against `usergroup members` output.
