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
