# cache · config commands

Global flags and the `--yes` gate convention: see the [commands index](../commands.md).
In-binary version: `agent-slack cache usage`, `agent-slack config usage`.
Full cache contract: [../output.md](../output.md).

| Command | Notes |
|---|---|
| `cache info` | what's cached per workspace: categories, entry counts, size, age (all workspaces unless `--workspace`) |
| `cache warm [users\|channels\|usergroups\|emoji\|dm-channels...] [--page-delay 1s] [--no-bots] [--stale-only]` | pre-fetch the named categories (all if none given) so completions + resolution are instant and offline, and arm the completeness sentinel (a later miss is authoritative within `cache.ttl.*-complete`, default 30m). Bots are warmed by default so the user set is complete; `--no-bots` excludes them but leaves the sentinel un-armed. `dm-channels` caches open-DM channel ids from the existing DM list (`conversations.list types=im`) — it never opens a new DM, and a `users` warm fills it for free. `--stale-only` skips categories still complete within the sentinel window (re-warm only what has gone stale — ideal for a repeated/scheduled warm; emits `skipped:true` for skipped categories). Paginates each endpoint, paced (`--page-delay 0` to disable); streams JSONL progress (filter `done:true` for the per-category summary) |
| `cache purge [--workspace … \| --all-workspaces] [--downloads]` | clear cached data (local + regenerable; no `--yes`). `--downloads` clears the downloaded-files cache (global — see below) |
| `config list` | persisted settings + the settable keys |
| `config get <key>` / `config set <key> <value>` / `config unset <key>` | read/write persisted settings |

The resolution cache (channel/user/workflow lookups, never message bodies)
fills from ordinary use and serves `get`/`list` from cache within a short
window (default 5m); completions and name→ID resolution use longer TTLs.
Persist a TTL with `config set cache.ttl.<category> <dur>` (categories:
`users`, `channels`, `channel-names`, `handles`, `dm-channels`, `workflow-list`,
`workflow-preview`, `workflow-schema`, `get`, `list`). Per-invocation:
`--no-cache`, `--refresh-cache`, `--cache-ttl`. See [../output.md](../output.md)
for the cache contract in full.

Downloaded files are **not** workspace-scoped: Slack file IDs (`F…`) are
globally unique and immutable, so the file ID is a sufficient, workspace-
independent key, and one flat `downloads/` dir naturally dedupes a file shared
across workspaces. So `cache purge --downloads` is global, while
`--workspace`/`--all-workspaces` scope only the resolution cache.
