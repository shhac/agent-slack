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
agent-slack channel mark  "D035EASSUH3" --ts "1770165109.628379"
```

When the target is a channel (name/ID, or channel URL with no message),
commands that act on one message (`get`, `edit`, `delete`, `react`, `mark`)
require `--ts "<seconds>.<micros>"`. `message list` shows recent ts values.

## 4. User ID (DM)

A `U…` user ID is a DM target: the DM auto-opens and the message goes there.

```bash
agent-slack message send U05BRPTKL6A "hi"          # DM auto-opens
agent-slack user dm-open @alice @bob                # group DM channel id
```

Some commands reject user targets (e.g. `channel mark`) — they return
`fixable_by: agent` telling you to use a channel or message URL instead.

## Multi-workspace disambiguation

With several workspaces configured, commands use the **default** workspace
(`auth set-default <url>`). To target another:

- pass `--workspace <unique-substring>` (URL, host, name, or any unique part), or
- use a permalink / channel URL, which carries its own workspace.

Channel **names** are workspace-relative, so resolve them against the default
or an explicit `--workspace`. Channel/DM **IDs** and URLs are unambiguous.
`SLACK_WORKSPACE_URL` (with `SLACK_TOKEN`) is the env-var equivalent of a
default.
