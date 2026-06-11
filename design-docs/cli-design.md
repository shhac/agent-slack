# CLI design: command surface, output, and LLM-first decisions

Decided after mapping the TS CLI (`../stablyai-agent-slack/src/cli`) against
`lin` (the family's best-practice reference for result formats, error hints,
and lazy data pulls). This doc is the contract for steps 3–6 of the port.

## Principles

1. **LLM-only.** No interactive prompts, no browser opening, no editors, no
   CI-mode special cases. If a feature exists for a human at a keyboard, it is
   out of scope (not deferred).
2. **Token economy.** Compact projections by default; bulky payloads behind
   `--full`; truncation with explicit markers; `--counts-only` where it exists
   in TS.
3. **Chainability.** Every output carries the IDs the next command needs
   (channel_id + ts everywhere; permalink where it's free to compute).
4. **Structured errors always.** JSON on stderr with `fixable_by` and a hint
   that names the exact follow-up command. Never a bare message.
5. **Behavior parity where agents see it** (targets, rendering, search
   syntax); Go conventions where they don't (package layout, typed mappers,
   cobra registration).

## Command tree

`--workspace/-w`, `--format/-f`, `--timeout/-t`, `--debug/-d`, `--full` are
global persistent flags (TS re-declared `--workspace` per command).

| Command | Key flags | Gate | Notes |
|---|---|---|---|
| `auth whoami` | | | implemented |
| `auth test` | | | calls `auth.test`; lands with read commands |
| `auth add / set-default / remove / import-* / parse-curl` | | | implemented |
| `message get <target>` | `--ts`, `--thread-ts`, `--max-body-chars` (8000), `--include-reactions`, `--resolve-users`, `--refresh-users`, `--no-download` | | thread summary included; files auto-downloaded |
| `message list <target>` | `--thread-ts`, `--ts`, `--limit` (25, max 200), `--oldest`, `--latest`, `--with-reaction`, `--without-reaction`, `--max-body-chars` (8000), `--download`, reaction/user flags as get | | NDJSON; reaction filters require `--oldest` |
| `message send <target> [text]` | `--thread-ts`, `--reply-broadcast`, `--attach` (repeatable), `--blocks` (path or `-`), `--schedule`, `--schedule-in` | | DM auto-opens for `U…` targets |
| `message edit <target> <text>` | `--ts` | `--yes` | |
| `message delete <target>` | `--ts` | `--yes` | |
| `message react add/remove <target> <emoji>` | `--ts` | | |
| `message scheduled list` | `--channel`, `--oldest`, `--latest`, `--limit`, `--cursor` | | NDJSON |
| `message scheduled cancel <id>` | `--channel` | `--yes` | destroys a pending send |
| `channel list` | `--user`, `--all`, `--limit` (100), `--cursor` | | NDJSON, compact projection |
| `channel new` | `--name`, `--private` | `--yes` | |
| `channel invite` | `--channel`, `--users`, `--external`, `--allow-external-user-invites` | `--yes` | |
| `channel mark <target>` | `--ts` | | mark-read; personal state |
| `user list` | `--limit` (200), `--cursor`, `--include-bots` | | NDJSON, compact projection |
| `user get <user>` | | | accepts `U…` or `@handle` |
| `user dm-open <users…>` | | | returns DM/group-DM channel id |
| `search all/messages/files <query>` | `--channel` (repeatable), `--user`, `--after`, `--before`, `--content-type`, `--limit` (20), `--max-content-chars` (4000), user-resolve flags | | NDJSON |
| `workflow list <channel>` | | | |
| `workflow preview <Ft…>` / `get <Ft…|Wf…>` | | | |
| `workflow run <Ft…>` | `--channel`, `--field Title=value` (repeatable) | | form submission needs browser auth + RTM WebSocket |
| `canvas get <canvas>` | `--max-chars` (20000) | | HTML→Markdown; dep chosen in this slice |
| `unreads` | `--counts-only`, `--max-messages` (10), `--max-body-chars` (4000), `--include-system` | | |
| `later list` | `--state`, `--limit` (20), `--max-body-chars` (4000), `--counts-only` | | NDJSON |
| `later save/complete/archive/reopen/remove` | `--ts` | | personal state, ungated |
| `later remind <target>` | `--in`, `--ts` | | duration parsing (30m, 2d, tomorrow…) |
| `file download <file-id>` | `--workspace` | | point pull to cache dir; new vs TS |
| `api call <method>` | `--params <json|->` | | raw escape hatch; new vs TS |
| `usage`, `<domain> usage` | | | see "usage system" |

## Mutation gating (`--yes`)

**Decision: gate destructive operations only.** Plain sends and reactions are
the tool's purpose and run ungated (like `lin`, which gates nothing). Gated:

- `message edit`, `message delete` — rewrite/remove existing content
- `message scheduled cancel` — destroys a pending send
- `channel new`, `channel invite` — create org-visible structure / change
  membership (external invites especially)

Ungated by decision: `message send`, `react add/remove`, `workflow run`,
`later *`, `channel mark`, `user dm-open`, `api call`.

Without `--yes`, a gated command returns `fixable_by: human` describing
exactly what would happen and a hint with the rerun command including `--yes`.

This supersedes the broader "all writes gated" wording in
`initial-design.md`/`AGENTS.md` (updated to match).

## Output contract

- **Lists → NDJSON** (one object per line), trailing
  `{"@pagination":{"has_more":true,"next_cursor":"…"}}` when more exist.
  TS printed one pretty-JSON blob everywhere; we follow the family convention.
- **Single resources → pretty JSON.** `--format json|yaml|jsonl` overrides.
- **Compact projections by default; `--full` returns the raw API payload.**
  TS dumped raw `conversations.list`/`users.list` responses (users.list
  profile blobs are huge); this is the biggest token win of the port.
  - channel: `id, name, is_private, is_im, is_mpim, is_archived, is_member,
    member_count, topic`
  - user: `id, name, real_name, display_name, is_bot, deleted, tz, email`
  - message: `render.CompactMessage` (already implemented)
  - search results / scheduled / later items: same compaction approach,
    fields fixed when each command is built
- **Truncation:** `--max-body-chars` defaults match TS (8000 message
  get/list; 4000 search/later/unreads; 20000 canvas; `-1` unlimited),
  truncated content ends with `\n…`.
- **Lazy pulls stay opt-in:** `--include-reactions`, `--resolve-users`
  (+ `--refresh-users`), `--download` (below). Thread summary on
  `message get` stays — one cheap call, high value.
- **Permalinks:** `message get` and `message send` outputs include
  `permalink` (computed locally via `render.BuildMessageURL`, no API call).
  List rows omit it to keep NDJSON lean; `channel_id` + `ts` chain into
  `message get`.
- All confirmations are JSON. (TS printed plain text for some auth imports.)

## File downloads

**Decision: `message get` downloads automatically; everything else is
metadata-only unless asked.**

- `message get`: auto-download to the cache dir (XDG
  `~/.cache/agent-slack/downloads`), `--no-download` to skip. You usually
  fetched one message to read its attachment.
- `message list` / `search` / `unreads`: emit file metadata only
  (`id, name, mimetype, mode, permalink`); `--download` opts in.
- `file download <file-id>`: point pull for a file seen in any listing
  (lin's lazy-pull pattern). Canvas-mode files convert to Markdown as in TS.
- Failed downloads surface an `error` field on the file entry, never abort
  the command (port-notes rule).

## Credentials: resolution and refresh

- Resolution order per invocation (unchanged): `--workspace` flag → env
  (`SLACK_TOKEN`, `SLACK_COOKIE_D`, `SLACK_WORKSPACE_URL`) → stored default.
- **No first-run auto-extraction.** TS silently tried Slack Desktop → Chrome
  → Brave → Firefox when nothing was configured; we return
  `fixable_by: human` with hint `run 'agent-slack auth import-desktop'`.
- **Desktop auto-refresh kept** (decision): on `invalid_auth`/`token_expired`,
  re-extract from Slack Desktop **for already-configured workspaces only**,
  retry the command once, note the refresh on stderr. Skipped when
  credentials came from env vars (mirrors TS). xoxc rotation is the #1
  failure mode; this makes it self-healing instead of human-fixable.

## `api call` escape hatch

`agent-slack api call <method> --params '<json>'` (or `--params -` for
stdin) posts to any Slack API method with stored credentials, printing the
raw response. **Decision: ungated** — it is an explicit power tool and the
caller typed the method name. The `usage` text says wrapped commands are
preferred. This is `lin api query` translated to Slack's method-call model.

## Errors and hints

`{error, fixable_by, hint?}` on stderr, exit code 1 (already scaffolded).
Conventions lifted from lin:

- Hints name the exact next command: `run 'agent-slack auth import-desktop'`,
  `pass --ts from 'message list' output`, `run 'agent-slack usage'`.
- Ambiguous `--workspace` errors enumerate the candidate workspaces.
- Unknown subcommands return a structured `fixable_by: agent` error listing
  the valid subcommands (lin's `HandleUnknownCommand`), not bare cobra help.
- Mapping: bad input → `agent`; auth/permissions/missing creds → `human`;
  429/5xx/network → `retry` (client layer maps these, step 3).
- `possiblyTruncated` permalinks (thread_ts without cid) warn on stderr that
  the shell likely ate `&cid=…` — port of message-url-warning.

## usage system

- `agent-slack usage`: ~1k-token overview — domains, target syntax, ID
  formats, pagination, truncation, error contract, gating, auth setup.
- `agent-slack <domain> usage` (message, channel, search, …): detailed
  per-domain docs with flags, defaults, and output field lists, written for
  an LLM reader (lin's per-domain usage pages are the model).
- Ship `skills/agent-slack/SKILL.md` in-repo, kept in sync with the surface.

## Not ported (decisions)

- **`message draft`** + draft HTTP server + embedded HTML editor + browser
  launching (`open`/`xdg-open`) + its CI mode. Only browser-opening feature
  in the TS CLI layer; LLM-first rules it out entirely.
- **`update`/`upgrade` self-update** — the fork already removed it;
  distribution is brew/`go install`.
- **First-run browser auto-extraction** (see Credentials).
- Plain-text output paths, interactive anything, zenity.

## Divergence ledger vs TS (quick reference)

| Area | TS | Go |
|---|---|---|
| List output | one pretty-JSON document | NDJSON + `@pagination` trailer |
| channel/user list payloads | raw Slack API responses | compact projections, `--full` for raw |
| Errors | bare `console.error` text | structured `APIError` JSON + hints |
| `--workspace` | declared per command | global persistent flag |
| Mutation gating | none | `--yes` on edit/delete/scheduled-cancel/new/invite |
| File downloads | auto on get+list+search | auto on get only; `--download`; `file download` |
| First-run creds | silent browser extraction chain | explicit `auth import-*` + hint |
| Raw API access | none | `api call` escape hatch |
| Self-docs | `--help` only | `usage` + per-domain usage + SKILL.md |

## Implementation order (refines port-order steps 3–5; all complete)

1. **Client + mockslack** (step 3): DI transport (browser + standard), 429
   retry/backoff, error mapping, auto-refresh hook, pagination helper,
   channel/user resolvers, per-run user cache.
2. **Read slice A**: `auth test`, `message get/list`, `channel list`,
   `user list/get` (+ compact mappers, usage pages as commands land).
3. **Read slice B**: `search *`, `unreads`, `later list`, `canvas get`
   (HTML→MD dep decided here), `file download`.
4. **Writes**: `message send/edit/delete/react/scheduled`, `channel
   new/invite/mark`, `user dm-open`, `later` mutations, `api call`.
5. **Workflows last**: `workflow list/preview/get/run`; `run --field` brings
   the RTM WebSocket dependency (`github.com/coder/websocket` — small,
   maintained, no transitive deps).

New dependencies taken: `github.com/coder/websocket` (workflow form
submission needs a short-lived RTM WebSocket; zero transitive deps) and
`github.com/JohannesKaufmann/html-to-markdown/v2` (canvas HTML→Markdown;
closest behavioral match to the TS original's turndown+GFM).
