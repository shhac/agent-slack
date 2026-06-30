# Identity-scoped cache & downloads

## Why

The on-disk cache and downloads need to be scoped to *who we are*, not just
*which workspace host* we point at. Two concrete failures motivated this:

1. **Stale-identity poisoning.** The cache used to key only by workspace
   hostname (`sha256(host)[:16]`). Several cached categories are per-user, not
   workspace-global: `dm-channels`, `drafts`, `scheduled`, `later`, `unreads`,
   and the channel list (which reflects the *viewer's* private-channel
   membership). Cache-warm as user A, then re-auth the same workspace as user B
   on the same machine, and B reads A's data. A correctness and soft-privacy
   bug.

2. **Unscoped downloads / emoji images.** `downloads/` and `emoji-images/` were
   flat and shared across every workspace.

This is a **correctness** boundary (right data for the right identity), **not a
security** boundary: two humans sharing one OS account already share the
filesystem. Namespacing prevents stale/wrong data; it does not isolate secrets.

## Identity = `(team_id, user_id)`

The owner id is the pair `(team_id, user_id)`:

- **`team_id`**, not the workspace URL: it is canonical and immutable; a
  workspace can rename its domain, which would silently re-key a URL/host
  scheme. `team_id`s are globally unique, so `enterprise_id` adds nothing to
  uniqueness and is omitted from the key. (On Enterprise Grid `team_id` is the
  per-workspace id we want — distinct workspaces in one org have distinct
  channel sets and must not share a namespace.)
- **`user_id`**: the fix for failure #1 — the per-user categories above.

### Where the ids come from

The only authoritative source is Slack's `auth.test` (a network call). Browser/
desktop extraction yields only URL/Name/Token; the credential file had a
`team_id` field but never populated it.

**Resolve once, persist, never re-resolve.** The ids are not secret, so they
live in `credentials.json` alongside the other non-secret workspace metadata
(`workspace_url`, `team_domain`), via `Store.SetIdentity`. The cache key is then
derived offline from the stored fields.

Resolution is **lazy on first use**, not at add time:

- `auth add` / import stay purely offline (no behaviour change, consistent with
  the `--form` keep-secrets-off-the-wire ethos). One identity-resolution path
  serves new, legacy, and env credentials.
- On a command that needs a client, if the stored `team_id`/`user_id` are
  missing, do a one-time bootstrap `auth.test` through the normal client (so
  browser auth's existing self-heal refreshes a rotated `xoxc`), persist, then
  build the cache with the resolved key. Steady state: zero extra calls.
- **Best-effort.** If the bootstrap `auth.test` fails (offline, transient,
  invalid), resolution returns empty, caching runs **inert** for that
  invocation, and we retry next time. We never block the command or guess a key.
- **Env-var credentials** (`SLACK_TOKEN`) persist nothing → resolve per
  invocation; inert on failure.

## Layout

```
cacheRoot = xdg.CacheDir("app.paulie.agent-slack")   # ~/.cache/app.paulie.agent-slack
cacheRoot/<team_id>/<user_id>/<category>.json        # resolution cache
cacheRoot/<team_id>/<user_id>/downloads/<file>       # downloaded files
cacheRoot/<team_id>/<user_id>/emoji-images/<sha>.png # decoded custom emoji
```

The cache key is the relative path `<team_id>/<user_id>` (both are Slack ids,
`T…`/`U…`, filesystem-safe; sanitised defensively). Empty when either id is
unknown → caching/dirs disable (inert), preserving the prior "no host → no
cache" behaviour.

Everything for an identity lives under one `<team_id>/<user_id>` directory, so
`auth remove` clears it in a single `RemoveAll` (categories + downloads + emoji).

### Roots from lib-agent-cli

`appCacheDir` delegates to `xdg.CacheDir` (the family root helper) instead of
hand-rolling XDG logic; the bespoke part is the `<team_id>/<user_id>` suffix.

### `/shared/` is deferred

Workspace-global categories (users, usergroups, emoji) could live under
`cacheRoot/<team_id>/shared/` to avoid a second user on the same workspace
re-fetching them. Deferred: the win is a few hundred KB for the rare
"two humans, one OS account, one workspace" case, while a misclassification
(e.g. putting the visibility-gated channel list in `shared/`) would leak
private-channel existence across users. Start correct-by-construction with
everything under `<user_id>/`; promote a vetted allowlist into `shared/` later
if re-warm cost ever bites. The nested layout is already the evolution path.

## Command & lifecycle effects

- **`cache info` / `cache purge`** map a stored workspace to its key via the
  persisted `(team_id, user_id)`, not the URL. The admin layer (list/inspect/
  purge) walks the two-level `<team>/<user>` layout.
- **`auth remove`** purges the removed workspace's identity directory.
- **completions** read the stored identity key (no network); empty on a cold
  credential.
- **MCP `fs` root** stays rooted at `cacheRoot` and is read-only. Downloads nest
  deeper under it (`<team>/<user>/downloads/…`) but remain reachable, and the
  download result reports the path, so the agent never needs to know its own
  identity. Root construction stays identity-free (it happens at startup, before
  any workspace is resolved).

## Migration

Old single-level `sha256(host)[:16]` cache dirs and the old flat
`downloads/`/`emoji-images/` become orphaned. They are regenerable, so cleanup
is best-effort: a one-time sweep (gated by a `cacheRoot/.layout-v2` sentinel)
removes legacy-format top-level entries. No data the user can't re-fetch is at
risk.
