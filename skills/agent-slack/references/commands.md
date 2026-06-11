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
| `channel new` | `--name`, `--private` | `--yes` |
| `channel invite` | `--channel`, `--users`, `--external`, `--allow-external-user-invites` | `--yes` |
| `channel mark <target>` | `--ts` | |

`channel invite --users` accepts user IDs and (with `--external`) email
addresses, comma-separated. `channel mark` is personal read state, ungated.

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
| `workflow list <channel>` | triggers (`Ft…`) published in a channel |
| `workflow preview <Ft…>` | trigger metadata + its workflow id (`Wf…`) |
| `workflow get <Ft…\|Wf…>` | form fields + step titles |
| `workflow run <Ft…> --channel <ch> --field "Title=value"` | submit a form; needs **browser auth** (xoxc/xoxd) + an RTM WebSocket |

Workflow discovery is channel-by-channel: a trigger listed in one channel may
still be un-previewable if you lack access (returns `fixable_by: human` with a
hint to ask a collaborator).

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
