# message commands

Global flags and the `--yes` gate convention: see the [commands index](../commands.md).
In-binary version: `agent-slack message usage`. Formatting: [../formatting.md](../formatting.md).

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

Text I/O is **standard Markdown** by default (both sending and reading): use
`[label](https://…)` for links (don't paste a bare URL and expect a nice link),
and `@name`/`@group` handles and `#channel` names resolve to real
mentions/links; `--slack-markdown` switches to Slack's native mrkdwn dialect.
Full table — links, mentions, escaping: [../formatting.md](../formatting.md).

`message get`/`list` also take `--format transcript` — a human-readable,
chronological text rendering (not JSON; errors still go to stderr as JSON, and
speakers/mentions/reactors always resolve to names). A
`──── <date> (<zone>) ────` divider opens each day, headers carry the time
only, consecutive messages from one author within 5 minutes collapse under one
header, and thread replies render as a `├─`/`└─` tree. Flags: `--tz
<Local|UTC|IANA>` (display zone, default `Local`, honors `$TZ`); `--with-ids`
appends each message's `ts` id; `--color <auto|always|never>` (default `auto`:
ANSI styling only when stdout is a TTY, honoring `NO_COLOR`/`CLICOLOR_FORCE`, so
the piped/LLM path stays plain).

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
