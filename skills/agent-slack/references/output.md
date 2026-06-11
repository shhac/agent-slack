# Output and downloads (reference)

## Shape

- **Lists → NDJSON**: one JSON object per line, then meta lines (below).
- **Single resources → pretty JSON**.
- `--format json|yaml|jsonl` overrides: `json`/`yaml` wrap a list in one
  envelope (`{"data": […], "@pagination": …}`); `jsonl` forces NDJSON.
- Empty values are pruned (`null`, `[]`, `{}` dropped where possible).
- **Errors → JSON on stderr**, non-zero exit:
  `{"error": "...", "fixable_by": "agent|human|retry", "hint": "..."}`.
  `agent` = fix the input and retry; `human` = a person must act
  (credentials/permissions/sharing); `retry` = transient, run again. A `hint`
  names the concrete next step when one exists.

## Meta lines (trailing `{"@key": …}` objects in NDJSON)

| Line | Meaning |
|---|---|
| `{"@pagination": {"has_more", "next_cursor"}}` | more pages exist; pass `--cursor <next_cursor>` |
| `{"@referenced_users": {"U…": {id, name, …}}}` | profile metadata for the `U…` ids in the items (only with `--resolve-users`) |
| `{"@channel_id": "C…"}` | the channel the listed messages came from |
| `{"@thread_ts": "…"}` | the thread root, when listing a thread |
| `{"@threads": {"has_unreads", "mention_count"}}` | unread thread-reply summary (`unreads`) |
| `{"@counts": {…}}` | totals when `--counts-only` is set (`unreads`, `later`) |

## Compact by default, `--full` for raw

Most read commands emit **compact projections** to save tokens; pass `--full`
for the raw Slack API payload.

- **channel**: `id, name, is_private, is_im, is_mpim, is_archived, is_member, member_count, topic`
- **user**: `id, name, real_name, display_name, is_bot, deleted, tz, email`
- **message**: `channel_id, ts, thread_ts?, author{user_id}, content, files?, reactions?`

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
- `auth list` → `{ default_workspace_url, credentials_path, workspaces: [{ workspace_url, auth_type, secrets: {token|xoxc|xoxd: "keychain"|"file"|"missing"}, hint? }] }`.

User IDs stay canonical in payloads (`author.user_id`, reaction `users[]`,
`@U…` mentions in rendered content). Pass `--resolve-users` to get an
`@referenced_users` map of display metadata; the per-workspace user cache has
a 24h TTL (`--refresh-users` bypasses it).

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

**Download location** (re-derivable cache; safe to purge):

```
$XDG_CACHE_HOME/app.paulie.agent-slack/downloads/   # if XDG_CACHE_HOME set
~/.cache/app.paulie.agent-slack/downloads/          # otherwise
```

## Resolution cache

Lookups that would otherwise be re-paid on every cold start are cached per
workspace under `<cacheDir>/<wshash>/<category>.json` (never message bodies):

| category | what | default TTL |
|---|---|---|
| `users` | user ID → profile | 24h |
| `handles` | @handle / email → user ID | 1h |
| `channel-names` | channel name → ID | 1h |
| `channels` | channel ID → metadata | 1h |
| `workflow-triggers` | `Ft…` → preview (workflow id, shortcut) | 1h |
| `workflow-schemas` | `Wf…` → form fields/steps | 1h |

The biggest win is channel-name → ID, which otherwise pages the whole
workspace. The cache is best-effort (never fails a command) and self-healing.

**Controls** (global flags / env):

- `--no-cache` (or `AGENT_SLACK_NO_CACHE=1`) — no read, no write.
- `--refresh-cache` — ignore cached reads but still write fresh entries.
- `--cache-ttl <dur>` (or `AGENT_SLACK_CACHE_TTL`) — override every category's
  TTL; `0` disables reads. Per-category override:
  `AGENT_SLACK_CACHE_TTL_<CATEGORY>` (e.g. `AGENT_SLACK_CACHE_TTL_CHANNELS=5m`).
- `--refresh-users` still forces a profile re-fetch on the read commands.

Rejections are never cached (a transient `trigger_not_found` won't stick), and
the side-effecting `workflow run` path is never cached.

Shell completions read these caches (install via `agent-slack completion
<shell>`, or Homebrew installs them automatically): completing a `<target>`
suggests channels and seen users from the cache, most-recently-used first,
capped and prefix-filtered. It is cache-only — never hits the API — so it is
empty on a cold cache and fills as you work.
