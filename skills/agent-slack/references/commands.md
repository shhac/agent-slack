# Commands (reference)

Full command map with the flags that matter. Global flags work on every
command: `--workspace/-w <substring>`, `--format/-f json|yaml|jsonl`,
`--timeout/-t <ms>`, `--debug/-d`, `--full`. Run `agent-slack <domain> usage`
for the in-binary version of any section.

**Gate column:** `--yes` means the command is destructive and refuses to run
without `--yes`; instead it returns a description of what *would* happen
(`fixable_by: human`). Show that to the user, then re-run with `--yes`.

## auth

| Command | Notes |
|---|---|
| `auth list` (aliases `ls`, `whoami`) | configured workspaces + where each secret is stored (`keychain`/`file`/`missing`); no secret material printed |
| `auth test` | calls Slack `auth.test` with the resolved credentials |
| `auth import-desktop` | extract xoxc/xoxd from Slack Desktop (best); macOS/Linux/Windows |
| `auth import-browser <name>` | from a browser — `chrome`, `brave` (running tab, macOS); `firefox`, `zen` (profile on disk, `--profile <sel>`); `opera` (profile on disk); `safari` (running tab + cookie store, macOS, needs Full Disk Access) |
| `auth parse-curl` | read a "Copy as cURL" Slack request on stdin, import its xoxc/xoxd |
| `auth add --workspace-url <url> (--token … \| --xoxc … --xoxd …)` | add credentials directly |
| `auth add --workspace-url <url> --form` | prompt for missing secrets via a native OS dialog (keeps tokens out of chat) |
| `auth set-default <url>` / `auth remove <url>` | manage the default workspace and stored secrets |

## message

| Command | Key flags | Gate |
|---|---|---|
| `message get <target>` | `--ts`, `--thread-ts`, `--max-body-chars` (8000), `--include-reactions`, `--resolve none\|cached\|auto\|fresh`, `--no-download`, `--slack-markdown` | |
| `message list <target>` | `--ts`, `--thread-ts`, `--limit` (25, max 200), `--oldest`, `--latest`, `--with-reaction`, `--without-reaction`, `--max-body-chars`, `--download`, `--slack-markdown`, + the resolve/reaction flags from `get` | |
| `message send <target> [text]` | `--thread-ts`, `--reply-broadcast`, `--attach` (repeatable; multiple files post as one message, text = shared comment), `--blocks <path\|->`, `--schedule <iso\|unix>`, `--schedule-in <30m\|2d\|tomorrow 9am>`, `--slack-markdown`, `--forward <permalink>` | |
| `message draft create <target> [text]` | `--blocks <path\|->`, `--slack-markdown`, `--forward <permalink>`, `--attach <path>` (repeatable; keeps rich text, unlike a direct attachment send) — returns a draft id | |
| `message draft list` | each row carries `id` + `file_ids` | |
| `message draft get\|edit\|send <target\|id>` | `edit`: `--blocks`, `--slack-markdown`, `--forward`, `--attach`; `send`: `--schedule`, `--schedule-in` | |
| `message draft delete <target\|id>` | | `--yes` |
| `message edit <target> [text]` | `--ts`, `--slack-markdown`, `--attach <path>` (repeatable), `--remove-attachment <F…>` (repeatable; ids from `message get` `files[].id`) — text optional when only changing attachments | `--yes` |
| `message delete <target>` | `--ts` | `--yes` |
| `message react add\|remove <target> <emoji>` | `--ts` | |
| `message scheduled list` | `--channel`, `--oldest`, `--latest`, `--limit`, `--cursor` | |
| `message scheduled cancel <id>` | `--channel` (required for bot/user tokens) | `--yes` |

`message list` reaction filters (`--with-reaction`/`--without-reaction`) only
apply to channel-history mode and require `--oldest` to bound the scan.

Text I/O is **standard Markdown** by default (both sending and reading);
`@name`/`@group` handles and `#channel` names resolve to real mentions/links;
`--slack-markdown` switches to Slack's native mrkdwn dialect. Full table:
[formatting.md](formatting.md).

`message send --forward <permalink>` forwards a message: any `[text]` becomes a
comment above it. **Same workspace only** — a permalink from another workspace
is a link, not a forward, and is rejected. On **browser (xoxc) auth** this posts
a real native forward card (`chat.shareMessage`): the content is embedded with a
"View conversation" control and no raw URL. On other tokens it falls back to
posting the permalink for Slack to unfurl — a permission-scoped card that
recipients without access to the source channel see only in reduced form.
`draft create`/`edit --forward` always use the permalink form (you can't share
into a draft), so the card appears when the human sends it.

`message draft` (browser auth only) is the LLM→human hand-off: save a draft for
the user to open, review, edit, and send. Drafts are **many-per-target** and
non-intrusive (they never pre-fill the user's compose box), so `create` returns
a draft id and never conflicts. `get`/`edit`/`delete`/`send` take a **draft id**
(`Dr…`) or a **target** — a target resolves only when it holds exactly one
draft, otherwise the command errors and lists the candidate ids to pass instead.
`send` posts the draft now (with its attachments) and removes it. `list` shows
every unscheduled draft — including any the user started in-app, which are
indistinguishable from ours — each with its `id` and `file_ids`; scheduled
messages live under `scheduled`.

On browser (desktop-imported) auth, **scheduled messages are also drafts**:
`message send --schedule*` creates a scheduled draft, `scheduled list` lists
them, and `scheduled cancel <id>` deletes one by its id (no `--channel` needed)
— and you can have many scheduled messages per target. Bot/user tokens use the
`chat.scheduleMessage` API instead, require `--channel` to cancel, and can't use
the `draft` group (drafts are a client feature).

## channel

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

## user

| Command | Notes |
|---|---|
| `user list` | `--limit` (200), `--cursor`, `--include-bots` |
| `user get <user…>` | accepts `U…`, `@handle`, or email; one → object, several → NDJSON (+ `{"@unresolved": […]}`) |
| `user dm-open <users…>` | returns the DM / group-DM channel id (up to 8 users) |

## usergroup

Workspace user groups (subteams, the `@group` you @-mention). Aliased `usergroups`.

| Command | Notes |
|---|---|
| `usergroup list` | `--include-disabled`; compact rows: `id` (S…), `handle`, `name`, `description`, `user_count`, and `channels`/`groups` (the group's **default** channels/subteams — members are auto-added) |
| `usergroup get <usergroup…>` | accepts `S…` or `@handle`; one → object, several → NDJSON (+ `{"@unresolved": […]}`) |
| `usergroup members <usergroup>` | user ids by default; `--resolve cached`/`fresh` for profiles, `--include-disabled` |

`channels` lists **all** the group's default channels — the CLI takes no view on
which is "best" to post in; choose per your use case. To answer "which groups am
I in?", check your user id (from `auth test`) against `usergroup members` output.

## emoji

Workspace **custom** emoji. These are for discovery — which custom names exist
and what aliases resolve to. To *use* an emoji in a message, just type
`:shortcode:`; Slack renders it (no command needed). The ~1.8k standard unicode
emoji are built in and not listed here, but `emoji get` falls back to them.

| Command | Notes |
|---|---|
| `emoji list` | `--full`; NDJSON sorted by name. Lean by default: `name` + `alias_for` (aliases). `--full` adds the image `url`. Custom set only |
| `emoji get <name…>` | `:colons:` optional; one → object, several → NDJSON (+ `{"@unresolved": […]}`). Unified lookup: custom → `{custom:true, url\|alias_for}`; alias followed one hop (→ `url` or `unicode`); standard name → `{unicode}`. Matched exactly (case-folded only; `-_+` not collapsed) |
| `emoji search <query>` | `--limit` (20, max 100), `--cursor`, `--full`; fuzzy-ranks **custom** emoji. Rows carry `match` (`exact\|prefix\|token_prefix\|contains\|fuzzy`) + `score`. Query is folded (case + `-_+` collapsed), so `parrot` finds `party-parrot`. Paginated via `{"@pagination":{next_cursor}}` |

Backed by the per-workspace `emoji` cache (24h TTL). `cache warm emoji` pre-fills
it; within the window a name miss is authoritative (no refetch).

## search

```
search messages <query>   # message hits
search files <query>      # file hits (auto-downloaded; local paths returned)
search all <query>        # both
```

Flags: `--channel` (repeatable), `--user`, `--after YYYY-MM-DD`,
`--before YYYY-MM-DD`, `--content-type any|text|image|snippet|file`,
`--limit` (20), `--max-content-chars` (4000), `--slack-markdown`, and
`--resolve none|cached|auto|fresh` (default `auto`; resolves referenced
users/channels/usergroups in hits, like `message get`).

## workflow

| Command | Notes |
|---|---|
| `workflow list <channel>` | triggers (`Ft…`) published in a channel; each row carries `stale: true` + `stale_reason` when its trigger can't be previewed (a lingering bookmark) |
| `workflow preview <Ft…>` | trigger metadata + its workflow id (`Wf…`) |
| `workflow get <Ft…\|Wf…>` | form fields + step titles |
| `workflow run <Ft…> --channel <ch> --field "Title=value"` | submit a form; needs **browser auth** (xoxc/xoxd) + an RTM WebSocket |

Workflow discovery is channel-by-channel. `workflow list` validates every
listed trigger in one batched call, so stale bookmarks (deleted workflows →
`stale_reason: trigger_not_found`) and inaccessible ones are flagged inline
rather than only failing when you `preview` them — trust a row without `stale`.
The whole annotated list is cached per channel, and validating it also warms
each live trigger's preview cache.

## other

| Command | Key flags | Gate |
|---|---|---|
| `unreads` | `--counts-only`, `--max-messages` (10), `--max-body-chars` (4000), `--include-system`, `--slack-markdown` | |
| `later list` | `--state`, `--limit` (20), `--max-body-chars` (4000), `--counts-only`, `--slack-markdown` | |
| `later save\|complete\|archive\|reopen\|remove <target>` | `--ts` | |
| `later remind <target>` | `--in <30m\|2d\|tomorrow 9am>`, `--ts` | |
| `canvas get <canvas>` | `--max-chars` (20000) | |
| `file download <file-id>` | `--workspace` | |
| `api call <method>` | `--params '<json>'\|<file>\|-`, `--multipart` | |

`api call` is the raw escape hatch — POST any Slack Web API method with stored
credentials. Prefer the wrapped commands; reach for `api call` only when no
wrapper exists.

## cache / config

| Command | Notes |
|---|---|
| `cache info` | what's cached per workspace: categories, entry counts, size, age (all workspaces unless `--workspace`) |
| `cache warm [users\|channels\|usergroups\|emoji...] [--page-delay 1s] [--no-bots] [--stale-only]` | pre-fetch the named categories (all if none given) so completions + resolution are instant and offline, and arm the completeness sentinel (a later miss is authoritative within `cache.ttl.*-complete`, default 30m). Bots are warmed by default so the user set is complete; `--no-bots` excludes them but leaves the sentinel un-armed. `--stale-only` skips categories still complete within the sentinel window (re-warm only what has gone stale — ideal for a repeated/scheduled warm; emits `skipped:true` for skipped categories). Paginates each endpoint, paced (`--page-delay 0` to disable); streams JSONL progress (filter `done:true` for the per-category summary) |
| `cache purge [--workspace … \| --all-workspaces] [--downloads]` | clear cached data (local + regenerable; no `--yes`). `--downloads` clears the downloaded-files cache (global — see below) |
| `config list` | persisted settings + the settable keys |
| `config get <key>` / `config set <key> <value>` / `config unset <key>` | read/write persisted settings |

The resolution cache (channel/user/workflow lookups, never message bodies)
fills from ordinary use and serves `get`/`list` from cache within a short
window (default 5m); completions and name→ID resolution use longer TTLs.
Persist a TTL with `config set cache.ttl.<category> <dur>` (categories:
`users`, `channels`, `channel-names`, `handles`, `workflow-list`,
`workflow-preview`, `workflow-schema`, `get`, `list`). Per-invocation:
`--no-cache`, `--refresh-cache`, `--cache-ttl`. See output.md for the cache
contract in full.

Downloaded files are **not** workspace-scoped: Slack file IDs (`F…`) are
globally unique and immutable, so the file ID is a sufficient, workspace-
independent key, and one flat `downloads/` dir naturally dedupes a file shared
across workspaces. So `cache purge --downloads` is global, while
`--workspace`/`--all-workspaces` scope only the resolution cache.
