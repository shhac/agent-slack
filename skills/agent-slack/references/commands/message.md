# message commands

Global flags and the `--yes` gate convention: see the [commands index](../commands.md).
In-binary version: `agent-slack message usage`. Formatting: [../formatting.md](../formatting.md).

| Command | Key flags | Gate |
|---|---|---|
| `message get <target>` | `--ts`, `--thread-ts`, `--max-body-chars` (8000), `--include-reactions`, `--resolve none\|cached\|auto\|fresh`, `--no-download`, `--slack-markdown` | |
| `message list <target>` | `--ts`, `--thread-ts`, `--limit` (25, max 200), `--oldest`, `--latest`, `--with-reaction`, `--without-reaction`, `--max-body-chars`, `--download`, `--slack-markdown`, + the resolve/reaction flags from `get` | |
| `message send <target> [text]` | `--thread-ts`, `--reply-broadcast`, `--attach` (repeatable; multiple files post as one message, text = shared comment), `--blocks <path\|->`, `--schedule <iso\|unix>`, `--schedule-in <30m\|2d\|tomorrow 9am>`, `--slack-markdown`, `--forward <permalink>` | |
| `message draft create <target> [text]` | `--blocks <path\|->`, `--slack-markdown`, `--forward <permalink>`, `--attach <path>` (repeatable; keeps rich text, unlike a direct attachment send), `--thread-ts <ts>` (draft a thread reply; or pass a message permalink as the target) — returns a draft id (+ `thread_ts` when threaded) | |
| `message draft list` | each row carries `id` + `file_ids` (+ `thread_ts` when threaded) | |
| `message draft get\|edit\|send <target\|id>` | `edit`: `--blocks`, `--slack-markdown`, `--forward`, `--attach`, `--thread-ts` (keeps the draft's current thread unless overridden); `send`: `--schedule`, `--schedule-in` — sends into the draft's thread | |
| `message draft delete <target\|id>` | | `--yes` |
| `message edit <target> [text]` | `--ts`, `--slack-markdown`, `--attach <path>` (repeatable), `--remove-attachment <F…>` (repeatable; ids from `message get` `files[].id`) — text optional when only changing attachments | `--yes` |
| `message delete <target>` | `--ts` | `--yes` |
| `message react add\|remove <target> <emoji>` | `--ts` | |
| `message scheduled list` | `--channel`, `--oldest`, `--latest`, `--limit`, `--cursor` | |
| `message scheduled cancel <id>` | `--channel` (required for bot/user tokens) | `--yes` |

`message list` accepts a `U…`/`@handle` target too: the DM auto-opens and its
history (or a thread within it) lists like any channel.

`message list` reaction filters (`--with-reaction`/`--without-reaction`) only
apply to channel-history mode and require `--oldest` to bound the scan.

Text I/O is **standard Markdown** by default (both sending and reading): use
`[label](https://…)` for links (don't paste a bare URL and expect a nice link),
and `@name`/`@group` handles and `#channel` names resolve to real
mentions/links; `--slack-markdown` switches to Slack's native mrkdwn dialect.
An **unlabeled link** — `[url](url)` or `<url>`, i.e. no distinct label — is
auto-upgraded to Slack's inline link **chip** (the scheme-stripped pill its own
composer makes when you paste a URL); and a **same-workspace message permalink**
in that same unlabeled form (or bare in text) becomes the richer inline
message-reference chip. A deliberately *labeled* link (`[label](url)`) always
stays a plain link. Full table — links, mentions, escaping:
[../formatting.md](../formatting.md).

`message get`/`list` also take `--format transcript` — a human-readable,
chronological text rendering (not JSON; errors still go to stderr as JSON).
Speakers, `<@U…>` mentions, reactors, `<#C…>` channels, `<!subteam^S…>` groups,
`slack://` user/channel deep-links, and `<!date^…>` tokens all resolve inline to
names/labels under `--resolve` (default `auto`; `none` leaves ids bare) — the
same cache-controlled machinery as the JSON `referenced_*` maps, but rewritten
in place. `unreads`/`later list`/`message draft list`/`get` take `--resolve` too.
A
`──── <date> (<zone>) ────` divider opens each day, headers carry the time
only, consecutive messages from one author within 5 minutes collapse under one
header, and thread replies render as a `├─`/`└─` tree (`message get` adds a dim
`└ thread: N replies · <permalink>` footer). `message draft list`/`get` accept
it too, rendering a `──── Drafts · N ────` digest of `<id> → #channel` blocks.
Flags: `--tz
<Local|UTC|IANA>` (display zone, default `Local`, honors `$TZ`); `--with-ids`
appends each message's `ts` id; `--color <auto|always|never>` (default `auto`:
ANSI styling only when stdout is a TTY, honoring `NO_COLOR`/`CLICOLOR_FORCE`, so
the piped/LLM path stays plain). `--images <off|auto|on>` draws custom emoji as
actual images: `off` (default), `auto` (on a Kitty-graphics TTY —
Ghostty/kitty/WezTerm), or `on` (force, e.g. a capable terminal the env
heuristic doesn't recognize) — **a hidden human convenience; agents should not
set it** (it emits image escape bytes a tool consumer can't read). `--hyperlinks
<off|auto|on>` likewise renders `[label](url)` links as OSC 8 terminal
hyperlinks (`off` default, `auto` on a TTY, `on` force) — **hidden, human-only;
agents should not set it.**

## Reading downloaded files (esp. over MCP)

`message get`/`list` (and `file download F…`, `search files`) download
attachments to the cache dir and report a local `path` in `files[].path`. How
you read those bytes depends on how you're running:

- **Plain CLI:** the `path` is a real host path — Read it directly.
- **MCP (`agent-slack mcp`):** the client has no filesystem, so the bridge
  rewrites each such `path` into a fetchable reference
  `{"@type":"file","root":"cache","path":"<team_id>/<user_id>/downloads/F….png"}`
  (the host path is never exposed; downloads nest under the identity). Pass the
  reference's `path` verbatim to the bridge's built-in **`fs`** tool:
  - `fs get cache <team_id>/<user_id>/downloads/F0BD….png` — returns the bytes.
    Images come back as MCP **image blocks** (the model sees the picture); text
    comes back verbatim; other binary as an embedded base64 resource. Files over
    a small inline limit return a structured error rather than flooding context.
  - `fs find cache -e png -e jpg` — search the cache for images.
  - `fs ls cache <team_id>/<user_id>/downloads` — list a directory.
  - `fs` is read-only and addresses everything **relative to the `cache` root**;
    `..` escapes and out-of-root symlinks are rejected.

The same `{root,path}` reference shape is what `fs find`/`ls` return, so a file
looks identical however you discovered it.

## Forwarding

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

A draft can be a **thread reply**: `create --thread-ts <ts>` (or pass a message
permalink as the target, which resolves to that message's thread root) addresses
the draft to a thread, and `send` posts the reply into it. The thread rides in
the draft itself — `get`/`list` surface `thread_ts`, and `edit` keeps it unless
`--thread-ts` overrides — so it survives across review and through a
`--schedule*` promotion.

On browser (desktop-imported) auth, **scheduled messages are also drafts**:
`message send --schedule*` creates a scheduled draft, `scheduled list` lists
them, and `scheduled cancel <id>` deletes one by its id (no `--channel` needed)
— and you can have many scheduled messages per target. Bot/user tokens use the
`chat.scheduleMessage` API instead, require `--channel` to cancel, and can't use
the `draft` group (drafts are a client feature).
