# cache · config commands

Global flags and the `--yes` gate convention: see the [commands index](../commands.md).
In-binary version: `agent-slack cache usage`, `agent-slack config usage`.
Full cache contract: [../output.md](../output.md).

| Command | Notes |
|---|---|
| `cache info` | what's cached per identity: categories, entry counts, size, age, downloads bytes (all identities unless `--workspace`) |
| `cache warm [users\|channels\|usergroups\|emoji\|dm-channels...] [--page-delay 1s] [--no-bots] [--stale-only]` | pre-fetch the named categories (all if none given) so completions + resolution are instant and offline, and arm the completeness sentinel (a later miss is authoritative within `cache.ttl.*-complete`, default 30m). Bots are warmed by default so the user set is complete; `--no-bots` excludes them but leaves the sentinel un-armed. `dm-channels` caches open-DM channel ids from the existing DM list (`conversations.list types=im`) — it never opens a new DM, and a `users` warm fills it for free. `--stale-only` skips categories still complete within the sentinel window (re-warm only what has gone stale — ideal for a repeated/scheduled warm; emits `skipped:true` for skipped categories). Paginates each endpoint, paced (`--page-delay 0` to disable); streams JSONL progress (filter `done:true` for the per-category summary) |
| `cache purge [--workspace … \| --all-workspaces] [--downloads]` | clear cached data (local + regenerable; no `--yes`). A plain purge clears one identity's resolution cache and keeps its downloads; `--downloads` also clears that identity's downloaded files (see below). `--all-workspaces` clears every identity's resolution cache and also sweeps any pre-identity-layout orphans (leaving no stranded cache). `auth remove <url>` clears a workspace's whole identity subtree |
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

Both the resolution cache and downloads are scoped by **identity**
(`<team_id>/<user_id>`, resolved from `auth.test` and stored in
`credentials.json`), under
`~/.cache/app.paulie.agent-slack/<team_id>/<user_id>/` — so re-authing a
workspace as a different user can't read the previous user's per-user data
(DMs, drafts, scheduled, channel membership), and a download from a private
channel one user can see isn't readable by another. A plain `cache purge`
clears only the resolution cache (keeps downloads); `--downloads` clears the
resolved identity's downloads (add `--all-workspaces` to clear every identity's
downloads — downloads are no longer a single global dir); `auth remove` clears
the whole identity subtree.
