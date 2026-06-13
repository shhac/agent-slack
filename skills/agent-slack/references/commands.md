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
| `auth import-chrome` / `import-brave` | from a logged-in Slack tab (macOS) |
| `auth import-firefox [--profile <name>]` | from a Firefox profile (macOS/Linux/Windows) |
| `auth parse-curl` | read a "Copy as cURL" Slack request on stdin, import its xoxc/xoxd |
| `auth add --workspace-url <url> (--token … \| --xoxc … --xoxd …)` | add credentials directly |
| `auth add --workspace-url <url> --form` | prompt for missing secrets via a native OS dialog (keeps tokens out of chat) |
| `auth set-default <url>` / `auth remove <url>` | manage the default workspace and stored secrets |

## message

| Command | Key flags | Gate |
|---|---|---|
| `message get <target>` | `--ts`, `--thread-ts`, `--max-body-chars` (8000), `--include-reactions`, `--resolve-users`, `--refresh-users`, `--no-download` | |
| `message list <target>` | `--ts`, `--thread-ts`, `--limit` (25, max 200), `--oldest`, `--latest`, `--with-reaction`, `--without-reaction`, `--max-body-chars`, `--download`, + the resolve/reaction flags from `get` | |
| `message send <target> [text]` | `--thread-ts`, `--reply-broadcast`, `--attach` (repeatable), `--blocks <path\|->`, `--schedule <iso\|unix>`, `--schedule-in <30m\|2d\|tomorrow 9am>` | |
| `message edit <target> <text>` | `--ts` | `--yes` |
| `message delete <target>` | `--ts` | `--yes` |
| `message react add\|remove <target> <emoji>` | `--ts` | |
| `message scheduled list` | `--channel`, `--oldest`, `--latest`, `--limit`, `--cursor` | |
| `message scheduled cancel <id>` | `--channel` (required) | `--yes` |

`message list` reaction filters (`--with-reaction`/`--without-reaction`) only
apply to channel-history mode and require `--oldest` to bound the scan.

## channel

| Command | Key flags | Gate |
|---|---|---|
| `channel list` | `--user`, `--all`, `--limit` (100), `--cursor` | |
| `channel get <channel>` | `--full` | |
| `channel members <channel>` | `--resolve-users`, `--refresh-users`, `--limit` (100), `--cursor` | |
| `channel new` | `--name`, `--private` | `--yes` |
| `channel invite` | `--channel`, `--users`, `--external`, `--allow-external-user-invites` | `--yes` |
| `channel mark <target>` | `--ts` | |

`channel get` returns one channel's metadata (topic, membership, member count,
archive state; `--full` for the raw object). `channel members` lists the user
IDs in a channel (chain into `user get`, or pass `--resolve-users` to expand to
profiles inline). `channel invite --users` accepts user IDs and (with
`--external`) email addresses, comma-separated. `channel mark` is personal read
state, ungated.

## user

| Command | Notes |
|---|---|
| `user list` | `--limit` (200), `--cursor`, `--include-bots` |
| `user get <user>` | accepts `U…` or `@handle` |
| `user dm-open <users…>` | returns the DM / group-DM channel id (up to 8 users) |

## search

```
search messages <query>   # message hits
search files <query>      # file hits (auto-downloaded; local paths returned)
search all <query>        # both
```

Flags: `--channel` (repeatable), `--user`, `--after YYYY-MM-DD`,
`--before YYYY-MM-DD`, `--content-type any|text|image|snippet|file`,
`--limit` (20), `--max-content-chars` (4000), plus the user-resolve flags.

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
| `unreads` | `--counts-only`, `--max-messages` (10), `--max-body-chars` (4000), `--include-system` | |
| `later list` | `--state`, `--limit` (20), `--max-body-chars` (4000), `--counts-only` | |
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
