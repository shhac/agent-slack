# CLI design: command surface, output, and LLM-first decisions

agent-slack's command surface, output contract, and LLM-first decisions,
following `lin` (the family's best-practice reference for result formats,
error hints, and lazy data pulls) for conventions.

## Principles

1. **LLM-only.** No interactive prompts, no browser opening, no editors, no
   CI-mode special cases. If a feature exists for a human at a keyboard, it is
   out of scope (not deferred).
2. **Token economy.** Compact projections by default; bulky payloads behind
   `--full`; truncation with explicit markers; `--counts-only` where
   applicable.
3. **Chainability.** Every output carries the IDs the next command needs
   (channel_id + ts everywhere; permalink where it's free to compute).
4. **Structured errors always.** JSON on stderr with `fixable_by` and a hint
   that names the exact follow-up command. Never a bare message.
5. **Stable, well-defined behavior where agents see it** (targets, rendering,
   search syntax); idiomatic Go where they don't (package layout, typed
   mappers, cobra registration).

## Command tree

`--workspace/-w`, `--format/-f`, `--timeout/-t`, `--debug/-d`, `--full` are
global persistent flags.

| Command | Key flags | Gate | Notes |
|---|---|---|---|
| `auth list` (alias `ls`, `whoami`) | | | implemented |
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
| `file download <file-id>` | `--workspace` | | point pull to cache dir |
| `api call <method>` | `--params <json|->` | | raw escape hatch |
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
  Follows the family convention.
- **Single resources → pretty JSON.** `--format json|yaml|jsonl` overrides.
- **Compact projections by default; `--full` returns the raw API payload.**
  Raw `users.list` profile blobs are huge; compact projections are the
  biggest token win.
  - channel: `id, name, is_private, is_im, is_mpim, is_archived, is_member,
    member_count, topic`
  - user: `id, name, real_name, display_name, is_bot, deleted, tz, email`
  - message: `render.CompactMessage` (already implemented)
  - search results / scheduled / later items: same compaction approach,
    fields fixed when each command is built
- **Truncation:** `--max-body-chars` defaults (8000 message
  get/list; 4000 search/later/unreads; 20000 canvas; `-1` unlimited),
  truncated content ends with `\n…`.
- **Lazy pulls stay opt-in:** `--include-reactions`, `--resolve-users`
  (+ `--refresh-users`), `--download` (below). Thread summary on
  `message get` stays — one cheap call, high value.
- **Permalinks:** `message get` and `message send` outputs include
  `permalink` (computed locally via `render.BuildMessageURL`, no API call).
  List rows omit it to keep NDJSON lean; `channel_id` + `ts` chain into
  `message get`.
- All confirmations are JSON.

## File downloads

**Decision: `message get` downloads automatically; everything else is
metadata-only unless asked.**

- `message get`: auto-download to the cache dir (XDG
  `~/.cache/app.paulie.agent-slack/downloads`), `--no-download` to skip. You
  usually fetched one message to read its attachment.
- `message list` / `search` / `unreads`: emit file metadata only
  (`id, name, mimetype, mode, permalink`); `--download` opts in.
- `file download <file-id>`: point pull for a file seen in any listing
  (lin's lazy-pull pattern). Canvas-mode files convert to Markdown.
- Failed downloads surface an `error` field on the file entry, never abort
  the command.

## Resolution cache

**Decision: persist repeated resolutions per workspace; never message bodies.**
The CLI cold-starts each invocation, so resolutions are re-paid every run.

- **Storage: JSON files**, one per workspace per category, under a
  per-workspace subdir `<cacheDir>/<wshash>/<category>.json`. Chosen over
  SQLite: these are tiny key→value maps, JSON has no cross-process lock
  contention (agents fan out), needs no schema/migration, and stays
  human-debuggable. (`modernc.org/sqlite` stays in the binary for cookie DBs
  only.) The subdir groups a workspace's caches and makes per-workspace purge
  one rmdir.
- **Categories + default TTL**: `users` ID→profile (24h); `handles`
  @handle/email→ID, `channel-names` name→ID, `channels` ID→meta,
  `workflow-list` channelID→annotated workflows, `workflow-triggers`
  Ft→preview, `workflow-schemas` Wf→schema (1h each). Stable data lasts a day;
  volatile name/membership mappings an hour.
- **`workflow list` validates + warms** (decision): the listing endpoints
  (`bookmarks.list`/`workflows.featured.list`) carry no liveness info, so a
  deleted-but-bookmarked trigger used to list fine and only fail on `preview`.
  `ListChannelWorkflows` now validates every listed trigger in ONE batched
  `workflows.triggers.preview` call — stale/inaccessible ones are flagged
  `stale`+`stale_reason` inline, and each live trigger's preview cache is
  warmed for free from the same response. Best-effort: if the batch call
  fails, the list returns unannotated rather than erroring. The whole
  annotated result is cached per channel, so a repeated `workflow list` is 0
  API calls instead of 2–3.
- **Generic batch cache** (`internal/slack/cache.go`): load-once/save-once
  snapshots with a per-T validator (the user batch resolver would otherwise
  regress into per-key file I/O + a write race). Best-effort throughout: a
  cache must never fail a command.
- **Wired via the Client** (`WithCache`, built once in `clientOptions`), so the
  many resolvers read `c.cache` + `c.currentAuth().WorkspaceURL` with no
  signature churn. Known blind spot: a standard-token env client without
  `SLACK_WORKSPACE_URL` has no host → caching no-ops (same as before).
- **Controls**: `--no-cache` (no read/write; `AGENT_SLACK_NO_CACHE`),
  `--refresh-cache` (skip reads, still write), `--cache-ttl` /
  `AGENT_SLACK_CACHE_TTL[_<CATEGORY>]` (0 disables reads). `--refresh-users`
  stays per-command (it also implies `--resolve-users`) and ORs with refresh.
- **Never cache**: message bodies, rejections (a transient `trigger_not_found`
  must not stick), or the side-effecting `workflow run` bookmark resolution.
- **Read-through on get/list, two freshness tiers** (decision): one stored
  `fetched_at`, two thresholds. Completions and name→ID resolution tolerate the
  long category TTLs (1h/24h); serving a `get`/`list` as fresh uses a short
  window (`get`/`list` TTLs, default 5m). `channel get` reads a dedicated
  `channel-info` cache (raw `conversations.info`, so it serves compact AND
  `--full`, and never the partial list-warmed entity record); `user get` serves
  from the users entity store (uniform). `channel list`/`user list` cache the
  page keyed by query (`conversations-pages`/`users-pages`) so a repeat within
  the window is free. Modes (`--no-cache`/`--refresh-cache`) apply throughout.
- **TTL precedence**: `--cache-ttl` flag > per-category env > global env >
  persisted `config set cache.ttl.<cat>` > built-in default.
- **`cache` and `config` are separate top-level commands** (decision): `cache
  info|purge` *operates on cached data*; `config get|set|list|unset` *persists
  settings* (the TTLs) in `config.json` beside `credentials.json`. Nesting purge
  under config read as "configure a purge," which it isn't. `gc` was rejected:
  entries are ignored past TTL and pruned on the next write, files are tiny, and
  `purge` covers a clean slate.

## Credentials: resolution and refresh

- Resolution order per invocation (unchanged): `--workspace` flag → env
  (`SLACK_TOKEN`, `SLACK_COOKIE_D`, `SLACK_WORKSPACE_URL`) → stored default.
- **No first-run auto-extraction.** There is no first-run auto-extraction:
  when nothing is configured we return `fixable_by: human` with hint
  `run 'agent-slack auth import-desktop'`.
- **Desktop auto-refresh kept** (decision): on `invalid_auth`/`token_expired`,
  re-extract from Slack Desktop **for already-configured workspaces only**,
  retry the command once, note the refresh on stderr. Skipped when
  credentials came from env vars. xoxc rotation is the #1
  failure mode; this makes it self-healing instead of human-fixable.
- **`auth add --form`** (decision): agents must never see or relay raw
  tokens. `--form` opens a native OS dialog (zenity) for whichever secret is
  missing; an `xoxc-` answer triggers a follow-up prompt for the `xoxd`
  cookie. The no-secret error hints agents toward `--form`.
- **Windows support** (decision): `import-desktop` and the file-based
  `import-browser` sources (firefox, zen, opera) work on Windows. Chromium
  cookie decryption uses the DPAPI scheme: AES-256-GCM key
  wrapped by `CryptUnprotectData` in the profile's `Local State`, `v10`
  values are `nonce(12)‖ciphertext‖tag(16)`. Only the DPAPI syscall is
  build-tagged (`dpapi_windows.go`); the GCM/Local State parsing is pure and
  unit-tested on every platform. DPAPI round-trip + end-to-end tests live in
  `dpapi_windows_test.go` and only run on Windows machines. App-bound
  ("APPB", Chrome 127+) keys are rejected with a parse-curl hint. Secrets
  fall back to the credentials file (no Windows Credential Manager yet).
- **Keep `modernc.org/sqlite`** (decision, ~4.2MB of the binary): it backs
  real functionality (cookie/localStorage DB reads incl. WAL sidecars) with
  zero runtime dependencies; shelling out to system `sqlite3` or a minimal
  reader was rejected (WAL handling risk on Firefox `cookies.sqlite`).

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
  429/5xx/network → `retry` (the client layer maps these).
- `possiblyTruncated` permalinks (thread_ts without cid) warn on stderr that
  the shell likely ate `&cid=…`.

## usage system

- `agent-slack usage`: ~1k-token overview — domains, target syntax, ID
  formats, pagination, truncation, error contract, gating, auth setup.
- `agent-slack <domain> usage` (message, channel, search, …): detailed
  per-domain docs with flags, defaults, and output field lists, written for
  an LLM reader (lin's per-domain usage pages are the model).
- Ship `skills/agent-slack/SKILL.md` in-repo, kept in sync with the surface.

## Out of scope (decisions)

- **No browser-based draft editor** / draft HTTP server / embedded HTML
  editor / browser launching (`open`/`xdg-open`) + its CI mode: LLM-first
  rules out browser-opening features entirely.
- **No self-update (`update`/`upgrade`) command**; distribution is
  brew/`go install`.
- **First-run browser auto-extraction** (see Credentials).
- Plain-text output paths, interactive terminal anything. (Native OS dialogs
  are the one sanctioned interaction: `auth add --form` prompts the human for
  a secret via zenity so tokens never transit the agent's conversation —
  superseding the earlier blanket "no zenity" call. Family precedent:
  agent-posthog.)

## Output & behavior choices (quick reference)

- **Lists:** NDJSON + `@pagination` trailer.
- **channel/user list:** compact projections, `--full` for raw.
- **Errors:** structured `APIError` JSON + hints.
- **`--workspace`:** global persistent flag.
- **Mutation gating:** `--yes` on edit/delete/scheduled-cancel/new/invite.
- **File downloads:** auto on get only; `--download`; `file download`.
- **First-run creds:** explicit `auth import-*` + hint.
- **Raw API access:** `api call` escape hatch.
- **Self-docs:** `usage` + per-domain usage + SKILL.md.

## Implementation order (all complete)

1. **Client + mockslack**: DI transport (browser + standard), 429
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
`github.com/JohannesKaufmann/html-to-markdown/v2` (canvas HTML→Markdown,
with GFM support).
