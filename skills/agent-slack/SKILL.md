---
name: agent-slack
description: |
  Slack CLI for AI agents: read permalinks/threads/history/unreads/later/
  canvases/workflows, search messages and files, download attachments, look
  up users, list/create channels, open DMs, send/edit/delete messages,
  react, schedule sends, and call raw Slack APIs.
when_to_use: |
  Use when the user asks to read or act on Slack: fetch a message URL, list
  a thread or channel history, check unreads or saved-for-later items,
  search Slack, send/edit/delete a message, react, invite to a channel, run
  a workflow, or fetch a canvas.
allowed-tools: Bash(agent-slack *) Read
---

# agent-slack

JSON in, JSON out, no interactivity. Lists are NDJSON (one object per line,
then `{"@pagination":…}` / `{"@referenced_users":…}` meta lines); single
resources are pretty JSON. Errors are JSON on stderr with
`fixable_by: agent|human|retry` and a `hint`.

Safety: read and search freely. Do not send, edit, delete, react, schedule,
invite, or create channels unless the user explicitly asked for that action.

## Setup (once)

```bash
agent-slack auth import-desktop            # from Slack Desktop — best, no need to quit
# …or, if you don't run Slack Desktop:
agent-slack auth import-browser firefox    # chrome|brave|firefox|zen|opera|safari
agent-slack auth test                      # verify
agent-slack auth set-default https://acme.slack.com   # if several workspaces
```

If no import works, `agent-slack auth add --workspace-url <url> --form` opens a
native OS dialog so the human enters the token without it appearing in chat.
Never ask the user to paste a token into the conversation, and never read
credentials out of the store yourself; every command authenticates internally.
For the full menu (per-browser caveats, bot tokens, cURL import) run
`agent-slack auth usage`.

Env override: `SLACK_TOKEN` (+ `SLACK_COOKIE_D` + `SLACK_WORKSPACE_URL` for
xoxc browser tokens). Expired browser tokens self-heal from Slack Desktop.

With multiple workspaces configured, commands use the default workspace;
pass `--workspace <unique-substring>` to target another. Message permalinks
carry their own workspace and override both.

## Reading

```bash
agent-slack message get "https://acme.slack.com/archives/C…/p1770165109628379"
agent-slack message get "#general" --ts "1770165109.628379"
agent-slack message list "#general" --limit 25
agent-slack message list "#general" --thread-ts "1770165109.628379"   # whole thread
agent-slack unreads --counts-only
agent-slack later list
agent-slack canvas get F08012345AB
```

Always quote permalinks — unquoted `&` truncates them in the shell.
`message get` includes a `permalink`, a `thread` summary `{ts,length}`, and
downloads attachments (local paths in `files[].path`; `--no-download` to
skip). Lists keep attachments metadata-only; add `--download` or
`agent-slack file download F…` for point pulls. Add `--resolve-users` to
expand `U…` ids to profiles, `--include-reactions` for reactions.

## Searching

```bash
agent-slack search messages "deploy failed" --channel "#ops" --after 2026-06-01
agent-slack search files "architecture diagram" --content-type image
```

File hits download automatically and report local `path`s you can Read.

## Writing

```bash
agent-slack message send "#general" "ship it :rocket:"
agent-slack message send U05BRPTKL6A "ping"                  # DM auto-opens
agent-slack message send "<permalink>" "replying in thread"
agent-slack message send "#general" "see attached" --attach ./report.md
agent-slack message send "#general" "later" --schedule-in "tomorrow 9am"
agent-slack message scheduled list
agent-slack message scheduled cancel Q012ABC --channel C012345678 --yes
agent-slack message react add "<permalink>" :eyes:
agent-slack message edit "<permalink>" "fixed wording" --yes
agent-slack message delete "<permalink>" --yes
agent-slack channel mark "<permalink>"                       # mark read up to here
```

Outbound text is auto-formatted: `@U…` becomes a real mention, `& < >` are
escaped, bullet/numbered lists become rich text. Destructive commands need
`--yes` (`message edit|delete`, `message scheduled cancel`,
`channel new|invite`); without it they return a description of what would
happen — show it to the user before retrying with `--yes`.

## Channels, users, workflows

```bash
agent-slack channel list                      # compact; --full for raw
agent-slack channel get "#general"            # one channel's metadata
agent-slack channel members "#general" --resolve-users   # who's in it
agent-slack user get @paul
agent-slack user dm-open @paul @sam           # group DM channel id
agent-slack workflow list "#ops"
agent-slack workflow get Ft0001               # form fields + steps
agent-slack workflow run Ft0001 --channel "#ops" --field "Summary=EU deploy failed"
```

## Escape hatch

```bash
agent-slack api call team.info
agent-slack api call conversations.history --params '{"channel":"C…","limit":5}'
```

## Cache

Channel/user/workflow lookups are cached per workspace and fill as you work, so
repeat reads are fast and completions populate. It's transparent; reach for
these only when you need to:

```bash
agent-slack cache info                         # what's cached, per workspace
agent-slack cache purge --workspace "#…"        # clear one workspace
agent-slack cache purge --downloads             # clear downloaded files
agent-slack config set cache.ttl.channels 30m   # persist a TTL
```

Per-invocation: `--no-cache`, `--refresh-cache`, `--cache-ttl <dur>`.

## More detail

For anything beyond the examples above, read the bundled references:

- [references/commands.md](references/commands.md) — full command map, flags, and which commands are `--yes`-gated
- [references/targets.md](references/targets.md) — permalink vs channel URL vs name/ID vs user-ID targeting, and multi-workspace rules
- [references/output.md](references/output.md) — NDJSON + meta-line contract, compact vs `--full`, payload shapes, and download paths

Live docs from the binary: `agent-slack usage` is the overview;
`agent-slack <domain> usage` (message, channel, search, workflow, later, …)
has per-domain docs.
