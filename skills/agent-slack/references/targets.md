# Targets: how to point at a channel or message (reference)

Most commands take a `<target>`. Four forms are accepted.

## 1. Message permalink (preferred when you have one)

```
https://<workspace>.slack.com/archives/<channel_id>/p<digits>[?thread_ts=…]
```

A permalink names its workspace *and* a specific message, so it overrides
`--workspace`. Always quote it — an unquoted `&` (from `?thread_ts=…&cid=…`)
is truncated by the shell.

```bash
agent-slack message get  "https://acme.slack.com/archives/C0123ABCD/p1770165109628379"
agent-slack message list "https://acme.slack.com/archives/C0123ABCD/p1770165109628379"   # whole thread
agent-slack message send "https://acme.slack.com/archives/C0123ABCD/p1770165109628379" "reply in thread"
agent-slack channel mark "https://acme.slack.com/archives/C0123ABCD/p1770165109628379"
```

## 2. Channel URL (no message segment)

```
https://<workspace>.slack.com/archives/<channel_id>
```

Treated as a channel target that pins its workspace, just like a permalink.
Useful for pasting a channel link straight from Slack.

```bash
agent-slack message list "https://acme.slack.com/archives/C0123ABCD" --limit 25
```

Because it names no message, `message get` / `message edit` / `react` on a
channel URL still require `--ts`.

## 3. Channel name or ID

- Bare name: `general` (no `#` needed; `#general` also works)
- ID: `C…` (channel), `G…` (group), `D…` (DM)

```bash
agent-slack message get   "general" --ts "1770165109.628379"
agent-slack message list  "C0123ABCD" --limit 25
agent-slack message react add "general" ":eyes:" --ts "1770165109.628379"
agent-slack channel mark  "D0A1B2C3D4E" --ts "1770165109.628379"
```

When the target is a channel (name/ID, or channel URL with no message),
commands that act on one message (`get`, `edit`, `delete`, `react`, `mark`)
require `--ts "<seconds>.<micros>"`. `message list` shows recent ts values.

## 4. User (DM): `U…` id or `@handle`

A `U…` user id or an `@handle` is a DM target — the DM auto-opens and the
command (send **or** `message list`) runs against it. `@handle` resolves to an
id wherever a user is accepted (message targets, `user get`, `user dm-open`,
`channel invite --users`).

```bash
agent-slack message send U05BRPTKL6A "hi"          # DM auto-opens
agent-slack message send @alice "hi"               # same, by handle
agent-slack message list @alice --limit 5          # read the DM's history
agent-slack user dm-open @alice @bob                # group DM channel id
```

Shell completion offers each user as `@handle`, its id, and the bare `handle`
(and each channel as `#name`, its id, and the bare `name`), so whatever you've
typed — `@al`, `al`, or a `U…`/`C…` prefix — has a matching candidate that
fills a resolvable value. A bare tab shows the primary form (`@handle` /
`#name`). Some commands reject user targets (e.g. `channel mark`) — they
return `fixable_by: agent` telling you to use a channel or message URL instead.

## Multi-workspace disambiguation

With several workspaces configured, commands use the **default** workspace
(`auth set-default <alias>`). To target another:

- pass `--workspace <alias>` (or a URL, host, name, or any unique substring —
  exact alias match wins), or
- use a permalink / channel URL, which carries its own workspace.

Channel **names** are workspace-relative, so resolve them against the default
or an explicit `--workspace`. Channel/DM **IDs** and URLs are unambiguous.
`SLACK_WORKSPACE_URL` (with `SLACK_TOKEN`) is the env-var equivalent of a
default.
