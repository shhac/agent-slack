# Output and downloads (reference)

## Shape

- **Lists → NDJSON**: one JSON object per line, then meta lines (below).
- **Entity gets (`user get`, `channel get`, `usergroup get`, `emoji get`) →
  NDJSON by default**: one result per id in input order — the record, or an
  `{"@unresolved":{…}}` line for an id that couldn't be resolved. Item-level
  misses exit 0; auth/network errors exit 1 with empty stdout.
- **Single-arg gets (`message get`, `message draft get`, `workflow get`,
  `canvas get`) → NDJSON by default** (one line); `--format json|yaml` returns the
  pretty object.
- **`config get <key>…` → NDJSON**, one line per key in input order — the value
  record or `{"@unresolved":{…}}` per key that has no config entry. Takes 1..N keys.
- `--format json|yaml|jsonl` overrides: on a single entity get `json`/`yaml`
  return the pretty object; on a multi-id get `json`/`yaml` collapse to
  `{"data": […], "@unresolved": […]}`; `jsonl` forces NDJSON. For lists,
  `json`/`yaml` wrap in `{"data": […], "@pagination": …}`.
- Empty values are pruned (`null`, `[]`, `{}` dropped where possible).
- **Errors → JSON on stderr**, non-zero exit:
  `{"error": "...", "fixable_by": "agent|human|retry", "hint": "..."}`.
  `agent` = fix the input and retry; `human` = a person must act
  (credentials/permissions/sharing); `retry` = transient, run again. A `hint`
  names the concrete next step when one exists.
- **Notices → JSON on stderr**, exit unaffected: `{"notice": "...", "hint": …}`.
  Non-fatal, informational — e.g. Slack rate-limit throttling (a 429 retry, or
  the terminal hit with a hint about the 1 req/min non-Marketplace tier on
  `conversations.history`/`.replies`) and credential auto-refresh. Distinguish
  from errors by key (`notice` vs `error`), not by stream; a `notice` line is
  not a failure.

## Meta lines (trailing `{"@key": …}` objects in NDJSON)

| Line | Meaning |
|---|---|
| `{"@pagination": {"has_more", "next_cursor"}}` | more pages exist; pass `--cursor <next_cursor>` |
| `{"@referenced_users": {"U…": {id, name, …}}}` | profile metadata for the `U…` ids in the items (with `--resolve` (auto by default)) |
| `{"@referenced_channels": {"C…": {id, name, …}}}` | metadata for the `C…` channel ids mentioned in content (with `--resolve` (auto by default)) |
| `{"@referenced_usergroups": {"S…": {id, handle, …}}}` | metadata for the `S…` usergroup ids mentioned in content (with `--resolve` (auto by default)) |
| `{"@channel_id": "C…"}` | the channel the listed messages came from |
| `{"@thread_ts": "…"}` | the thread root, when listing a thread |
| `{"@threads": {"has_unreads", "mention_count"}}` | unread thread-reply summary (`unreads`) |
| `{"@counts": {…}}` | totals when `--counts-only` is set (`unreads`, `later`) |
| `{"@unresolved": {id,reason,fixable_by,hint?}}` | an id that couldn't be resolved in `user get` / `channel get` / `usergroup get` / `emoji get`; emitted **in-position** (one line per input, not a trailing aggregate) — item-level miss, exit 0 |

## Compact by default, `--full` for raw

Most read commands emit **compact projections** to save tokens; pass `--full`
for the raw Slack API payload.

- **channel**: `id, name, is_private, is_im, is_mpim, is_archived, is_member, member_count, topic`
- **user**: `id, name, real_name, display_name, is_bot, deleted, tz, email`
- **usergroup**: `id, handle, name, description, user_count, channels, groups` — `channels`/`groups` are the group's *default* channels/subteams (members are auto-added); the CLI lists them all and takes no view on which is "best" to post in
- **message**: `channel_id, ts, thread_ts?, author{user_id}, content, files?, reactions?` — each `files[]` entry has `id` (the `F…` to pass to `message edit --remove-attachment`), `name`, `mimetype`, and `path` once downloaded

The body field is `content`, **not** `text`: one rendered-Markdown string
merging Slack's raw `text`, blocks, and attachment/app-card unfurls (it is
deliberately not the raw API `text`, which is only a notification fallback and
often empty). A message without `content` genuinely has no text body —
re-fetching another way won't reveal more; `--full` shows the raw payload.

Message bodies are capped by `--max-body-chars` (defaults: 8000 for
get/list, 4000 for search/later/unreads, 20000 for canvas; `-1` = unlimited).
Truncated content ends with `\n…`.

## Key payload shapes

- `message get` → `{ "message": {…}, "permalink": "https://…", "thread"?: {ts, length} }`
  (`permalink` and `thread` are **top-level**, beside `message`).
- `message send` → `{ ok, channel_id, ts?, thread_ts?, permalink?, scheduled_message_id?, post_at? }`
  (`ts`/`permalink` absent on file-attachment sends; `scheduled_message_id`/`post_at` present for scheduled sends).
- `channel invite` (internal) → `{ channel_id, invited_user_ids, already_in_channel_user_ids?, unresolved_users? }`;
  (external, `--external`) → `{ channel_id, external: true, invited_emails, already_invited_emails?, invalid_external_targets? }`.
- `auth list` → `{ default_workspace, credentials_path, workspaces: [{ alias, workspace_url, auth_type, secrets: {token|xoxc|xoxd: "keychain"|"file"|"missing"}, hint? }] }`.

Entity IDs stay canonical in payloads (`author.user_id`, reaction `users[]`, and
`@U…`/`<#C…>`/`<!subteam^S…>` mentions in rendered content — rich_text carries
the bare id, no label). Message reads expand every referenced user, channel, and
usergroup into `@referenced_users` / `@referenced_channels` /
`@referenced_usergroups` maps **by default** (`--resolve auto`); `--resolve none`
skips it, `cached` is cache-only, `fresh` bypasses the
caches.

## Attachment downloads

- `message get` auto-downloads attachments; each file gains a `path` (absolute
  local path you can Read). `--no-download` skips.
- `message list` / `search messages` / `unreads` emit file **metadata only**
  (`id, name, mimetype, mode, permalink`); add `--download` to pull them.
- `search files` downloads hits automatically and returns their `path`s.
- `file download <file-id>` is a point pull for any file id seen in a listing.
- Failed downloads don't abort the command: the file entry keeps an `error`
  field and `path` points at a local `.download-error.txt` describing it.
- Canvas-mode files convert to Markdown.

**Download location** (re-derivable cache; safe to purge). Scoped by identity
(`<team_id>/<user_id>`), so the path includes that subdir:

```
$XDG_CACHE_HOME/app.paulie.agent-slack/<team_id>/<user_id>/downloads/   # if XDG_CACHE_HOME set
~/.cache/app.paulie.agent-slack/<team_id>/<user_id>/downloads/          # otherwise
```

Use the `path` each download result reports rather than constructing it — for
MCP, pass the `{"@type":"file","root":"cache","path":…}` the result carries.

## Resolution cache

Lookups that would otherwise be re-paid on every cold start are cached per
identity under `<cacheDir>/<team_id>/<user_id>/<category>.json` (never message
bodies). The `team_id`/`user_id` come from `auth.test` and are stored in
`credentials.json`, so re-auth as a different user gets a clean namespace:

| category | what | default TTL |
|---|---|---|
| `users` | user ID → profile | 24h |
| `handles` | @handle / email → user ID | 1h |
| `dm-channels` | user id(s) → DM/group-DM channel id (lazy on open; warmed from the open-DM list, never by opening one) | 24h |
| `channel-names` | channel name → ID | 1h |
| `channels` | channel ID → metadata (completions/resolution) | 1h |
| `channel-info` | channel ID → full `conversations.info` (serves `channel get`) | 5m |
| `usergroups` | @handle → subteam ID | 24h |
| `usergroup-entities` | subteam ID → metadata (completions, `usergroup get/list`) | 24h |
| `conversations-pages` / `users-pages` / `usergroups-pages` | a `channel list`/`user list`/`usergroup list` page, keyed by query | 5m |
| `workflow-list` | channel ID → its workflows (annotated) | 1h |
| `workflow-triggers` | `Ft…` → preview (workflow id, shortcut) | 1h |
| `workflow-schemas` | `Wf…` → form fields/steps | 1h |

Two freshness tiers off one timestamp: completions and name→ID resolution
tolerate the long category TTLs (1h/24h); **serving a `get`/`list` from cache
uses a short window** (the `get`/`list` TTLs, default **5m**). So `channel get`
/ `user get` and a repeated `channel list`/`user list` are served from cache
within 5m (great for a workflow hammering the same calls), while completions
still draw on the longer-lived entries. The cache fills from ordinary use —
every resolution writes through, and list/get warm the entity caches page by
page.

**Controls** (global flags / env / persisted config):

- `--no-cache` (or `AGENT_SLACK_NO_CACHE=1`) — no read, no write.
- `--refresh-cache` — ignore cached reads but still write fresh entries.
- TTLs, highest precedence first: `--cache-ttl <dur>` (all categories) >
  `AGENT_SLACK_CACHE_TTL_<CATEGORY>` > `AGENT_SLACK_CACHE_TTL` (all) >
  `config set cache.ttl.<category> <dur>` (persisted) > built-in default. `0`
  disables reads for a category. Categories include `get` and `list` (the 5m
  serve windows). `--resolve fresh` still forces a profile re-fetch.

Individual rejections are never cached (a transient `trigger_not_found` won't
stick), and the side-effecting `workflow run` path is never cached.

**Completeness sentinel (authoritative misses).** When a full enumeration of a
category finishes — `cache warm` (channels/usergroups always; users too unless
`--no-bots`, which leaves the set incomplete), or a resolution that paginated to
the end — the category is
stamped complete. Within a per-category **completeness window** (default
**30m**, keys `cache.ttl.users-complete` / `channels-complete` /
`usergroups-complete`) a later **miss is treated as authoritative**: the
`@handle` / `@group` / `#channel` is taken as absent without a remote lookup
(an `@`/`#` mention stays literal; a channel *target* errors). This turns a
message with many unknown references from one lookup per miss into a single
warm. It is **not** a negative cache — it records "we held the complete set as
of T," so it's bounded by the window. Newly-created entities therefore read as
absent until the window expires or `--refresh-cache`/`--no-cache` is used
(both bypass the sentinel). The window is independent of the `list` TTL — a
`list` still re-fetches on its own 5m cadence.

**Managing the cache:** `agent-slack cache info` shows what's cached per
workspace (entries, size, age); `cache warm [users|channels|usergroups...]`
pre-fetches the named categories (all if none given; paced for rate limits,
streams JSONL progress) so completions and resolution are instant and offline,
and arms the completeness sentinel; `cache purge [--workspace … |
--all-workspaces]` clears it (local + regenerable). `agent-slack
config set/get/list/unset` persists the TTLs above.

Shell completions read these caches (install via `agent-slack completion
<shell>`, or Homebrew installs them automatically), most-recently-used first,
capped and prefix-filtered. Suggestions are kind-appropriate: a `<target>`
(message get/list/send/edit/delete, channel mark, later remind) suggests
channels and DM users; `workflow list` and `--channel` flags suggest channels;
`user get`/`dm-open` suggest users; `usergroup get`/`members` suggest cached
subteams (`@handle`/id/`handle`); `workflow preview/get/run` suggest cached
`Ft…` triggers (with the workflow name as the hint). Channels and users are
offered in every form — `#name`/id/`name` and `@handle`/id/`handle` — so any
prefix style matches; a bare tab shows the primary (`#name`/`@handle`).
Cache-only — never hits the API — so it is empty on a cold cache and fills as
you work.
