package cli

// Static LLM-facing help content for `usage` and `<domain> usage`, split out
// from the command wiring in usage.go so doc edits do not churn the wiring.

const usageText = `agent-slack: Slack CLI for AI agents. JSON in, JSON out, no interactivity.

COMMANDS
  auth       list | test | add | set-default | remove | import-desktop |
             import-browser <name> | parse-curl
  message    get | list | send | edit* | delete* | react add/remove |
             scheduled list/cancel*
  channel    list | get | members | new* | invite* | mark
  user       list | get | dm-open
  usergroup  list | get | members — workspace subteams (@group)
  emoji      list | get | search — workspace custom emoji (:shortcode:)
  search     all | messages | files
  workflow   list | preview | get | run
  canvas     get
  unreads    (top-level) unread messages across channels/DMs/threads
  later      list | save | complete | archive | reopen | remove | remind
  file       download <F…> — point-pull a file seen in any output
  cache      info | warm | purge — inspect, pre-fill, or clear the cache
  config     get | set | list | unset — persist settings (cache TTLs)
  api        call <method> — raw Slack API escape hatch
  usage      this overview; '<domain> usage' has per-domain detail
  * = destructive: requires --yes, otherwise returns what WOULD happen

TARGETS
  Most message commands take a <target>: a Slack permalink
  (https://…/archives/C…/p1770165109628379 — quote it, '&' breaks shells),
  #channel / channel / C…, or U… for DMs. Channel-name/ID targets need
  --ts "<seconds>.<micros>" to name a specific message.

OUTPUT
  Lists are NDJSON: one object per line, then meta lines like
  {"@pagination":{"next_cursor":"…"}} and {"@referenced_users":{…}}.
  Single resources are pretty JSON. --format json|yaml|jsonl overrides.
  channel/user lists are compact projections; --full returns raw payloads.
  Bodies truncate with a trailing … at --max-body-chars
  (message 8000, search/later/unreads 4000, canvas 20000; -1 = unlimited).

CHAINING
  message get/send output a permalink; list rows carry channel_id + ts that
  chain into message get --ts. File metadata carries F… ids for
  'file download'. message reads resolve referenced users/channels/usergroups
  into referenced_* maps BY DEFAULT (--resolve auto: cache, fetch misses, hint to
  'cache warm'); --resolve none turns it off, cached is cache-only, fresh refetches.
  --include-reactions opts into reactions.

CACHE
  Awkward resolutions (channel name→ID, @handle→ID, profiles, workflow
  metadata) are cached per workspace under ~/.cache/app.paulie.agent-slack/.
  Never message bodies. get/list serve from cache within a short window
  (5m); completions/resolution use longer TTLs. --no-cache bypasses;
  --refresh-cache re-fetches but still writes. Tune TTLs via --cache-ttl,
  AGENT_SLACK_CACHE_TTL[_<CATEGORY>], or 'config set cache.ttl.<cat>'.
  'cache info' / 'cache purge' inspect and clear it; 'cache warm
  [users|channels|usergroups|emoji]' pre-fetches list endpoints (paced, streams
  JSONL) so completions/resolution are instant and offline (and --resolve auto
  is free), and arms a completeness sentinel — within cache.ttl.*-complete (30m)
  a later miss is authoritative (no remote lookup); --refresh-cache bypasses it.
  'cache warm --stale-only' re-warms only categories whose sentinel has lapsed.

ERRORS
  JSON on stderr: {"error","fixable_by","hint"}. fixable_by=agent → fix the
  input and retry; human → credentials/permissions need a person;
  retry → wait and re-run. Non-fatal notices also go to stderr as
  {"notice","hint"} (e.g. Slack rate-limit throttling) — distinguish by key
  ("error" vs "notice"), not by stream.

AUTH
  Stored per workspace (OS keychain where available). Setup: 'auth
  import-desktop' (or import-browser <name>, parse-curl, add; 'auth
  add --form' opens a native dialog so a human can enter a token without it
  ever appearing in chat). Env override: SLACK_TOKEN
  (+ SLACK_COOKIE_D + SLACK_WORKSPACE_URL for xoxc browser tokens).
  Multiple workspaces: pass --workspace <substring> or 'auth set-default'.
  Expired browser tokens self-heal from Slack Desktop mid-command.

Run 'agent-slack <domain> usage' for detailed per-domain documentation.
`

var domainUsage = map[string]string{
	"message": `agent-slack message — read and write messages.

GET    message get <target> [--ts …] [--thread-ts …]
       One message + thread summary {ts,length} + permalink. Files
       auto-download to the cache dir (paths in files[].path; --no-download
       skips). Flags: --max-body-chars 8000, --include-reactions,
       --resolve none|cached|auto|fresh.
LIST   message list <target>
       Channel target → recent history (--limit 25 max 200, --oldest,
       --latest), chronological NDJSON + {"@channel_id":…} meta line.
       Thread permalink or --thread-ts/--ts → the whole thread (rows drop
       channel_id/thread_ts; they're in meta lines). Reaction filters:
       --with-reaction/--without-reaction (repeatable, need --oldest).
       Files are metadata-only unless --download.
SEND   message send <target> [text] [--thread-ts …] [--reply-broadcast]
       Targets: #channel, C…, U… (DM auto-opens), or a permalink (replies in
       that thread). Text is standard Markdown: **bold**, *italic*/_italic_,
       ~~strike~~, __underline__ (extension), inline + fenced code, [label](url),
       - bullets, 1. numbers, > quotes; backslash escapes a literal marker.
       Mentions: @here/@channel, @U… ids, and @name / @group handles all resolve.
       #channel-name resolves to a channel link (a known channel only; "# " stays
       a literal, and all-digit "#5" refs are left alone).
       --slack-markdown interprets text as Slack mrkdwn (*bold*, <url|label>).
       --attach <path> (repeatable; multiple files share one message and the
       text becomes their single comment), --blocks <file|-> raw Block Kit,
       --schedule <iso8601-with-tz|unix>, --schedule-in <30m|2d|tomorrow 9am>.
       --forward <permalink> forwards that message (text becomes an optional
       comment); same workspace only — a cross-workspace URL is a link, not a
       forward. Browser (xoxc) auth posts a native forward card; other tokens
       fall back to a permalink unfurl (permission-scoped to the source channel).
       Output includes ts + permalink. Reads (get/list/search/unreads/later)
       return Markdown too; --slack-markdown keeps native Slack mrkdwn.
DRAFT  message draft create <target> [text] [--blocks <file|->] [--forward <permalink>] [--attach <path>]
       message draft list | get <target|id> | edit <target|id> [text] | send <target|id>
       --attach (repeatable) attaches files to the draft, keeping its rich text
       (links/formatting) — unlike a direct attachment send, which posts plain text.
       message draft delete <target|id> --yes   (destructive)
       LLM→human hand-off (browser auth): save a draft for the user to review
       and send. create returns a draft id; drafts are many-per-target, so
       get/edit/delete/send take an id or a target (when it has just one).
       'send' posts the draft now (with files) then removes it.
EDIT   message edit <target> [text] --yes     (destructive)
       add/remove attachments with --attach <path> / --remove-attachment <F…>
       (repeatable); text becomes optional when only changing attachments.
       Get attachment ids from 'message get' (files[].id).
DELETE message delete <target> --yes          (destructive)
REACT  message react add|remove <target> <emoji>   (:rocket:, rocket, or 🚀)
SCHED  message scheduled list [--channel …] [--cursor …]
       message scheduled cancel <id> [--channel <…>] --yes   (destructive)
       Browser auth: scheduled messages are drafts (cancel by id, no
       --channel). Bot/user tokens: --channel required to cancel.`,

	"channel": `agent-slack channel — conversations.

LIST   channel list [--user U…|@handle] [--all] [--limit 100] [--cursor …]
       Default: the authed user's conversations. Compact rows: id, name,
       is_private/is_im/is_mpim, is_member, num_members, topic; --full = raw.
GET    channel get <channel…> [--full] — channel metadata. One arg → object;
       several → NDJSON, with a trailing {"@unresolved": […]} for any misses.
MEMBERS channel members <channel> [--resolve none|cached|auto|fresh] [--limit] [--cursor]
       Who is in the channel: user ids (chain into 'user get'), or profiles
       with --resolve cached/auto/fresh.
NEW    channel new --name <name> [--private] --yes        (requires --yes)
INVITE channel invite --channel <…> --users "U…,@a,b@x.com" --yes
       --external sends Slack Connect email invites
       (--allow-external-user-invites lets invitees invite others).
       Output: invited/already-in/unresolved lists.
MARK   channel mark <target> [--ts …] — mark read up to a message.`,

	"user": `agent-slack user — directory.

LIST     user list [--limit 200] [--cursor …] [--include-bots]
         Compact rows: id, name (handle), real_name, display_name, email,
         title, tz, dm_id (open DM channel if one exists).
GET      user get <U…|@handle|email …> — one arg → object; several → NDJSON,
         with a trailing {"@unresolved": […]} for inputs that didn't resolve.
DM-OPEN  user dm-open <users…> — open a DM or group DM (max 8); returns
         dm_channel_id to send into.`,

	"usergroup": `agent-slack usergroup — user groups (subteams, @group).

LIST     usergroup list [--include-disabled] [--limit 200] [--cursor …]
         Compact rows: id (S…), handle, name, description, user_count, and
         channels/groups (the group's DEFAULT channels/subteams — members are
         auto-added to them). The CLI surfaces all default channels and takes
         no view on which is "best" to post in; pick per your use case.
         Paginated: a full page emits {"@pagination":{next_cursor}} — pass it
         to --cursor for the next page.
GET      usergroup get <S…|@handle …> — one arg → object; several → NDJSON,
         with a trailing {"@unresolved": […]} for inputs that didn't resolve.
MEMBERS  usergroup members <S…|@handle> [--resolve none|cached|auto|fresh] [--include-disabled]
         Who is in the group: user ids (chain into 'user get'), or profiles
         with --resolve cached/auto/fresh. To answer "which groups am I in?", scan
         'usergroup list' membership (or check 'auth test' user id against
         members).`,

	"emoji": `agent-slack emoji — workspace custom emoji (:shortcode:).

Custom emoji are sent as literal :shortcode: text — Slack renders them, so no
special handling is needed in message bodies. These commands are for DISCOVERY:
which custom names exist, and what an alias resolves to. The standard unicode
set is handled separately (built in); 'emoji get' falls back to it.

LIST     emoji list [--full] [--limit 200] [--cursor …]
         NDJSON of the workspace's CUSTOM emoji, sorted by name. Lean by
         default: name plus alias_for (for aliases). --full adds the image url.
         Does NOT include the ~1.8k standard unicode emoji. Paginated (a busy
         workspace can have thousands): a full page emits
         {"@pagination":{next_cursor}} — pass it to --cursor for the next page.
GET      emoji get <name…> — :colons: optional; one arg → object, several →
         NDJSON with a trailing {"@unresolved": […]}. Unified lookup over
         custom then standard emoji: custom → {custom:true, url|alias_for};
         alias → followed one hop (url or unicode); a standard name → {unicode}.
         Names are matched EXACTLY (case-folded only; -_+ not collapsed).
SEARCH   emoji search <query> [--limit 20] [--cursor …] [--full]
         Fuzzy-rank CUSTOM emoji by name. Tiers (high→low score): exact, prefix,
         token_prefix (matches a -_+-delimited token, so 'parrot' finds
         'party-parrot'), contains, fuzzy (edit distance). Each row carries
         {match, score}. Unlike get, the query is folded (case + -_+ collapsed).
         Paginated: a full page emits {"@pagination":{next_cursor}} — pass it to
         --cursor for the next page.
CACHE    Backed by the per-workspace 'emoji' cache (24h). 'cache warm emoji'
         pre-fills it; within the window a name miss is authoritative.`,

	"search": `agent-slack search — messages and files.

  search all|messages|files <query>
  Filters: --channel (repeatable; falls back to scanning channel history —
  catches messages Slack's search index misses), --user, --after/--before
  YYYY-MM-DD, --content-type any|text|image|snippet|file, --limit 20,
  --max-content-chars 4000.
  Message hits include a permalink for chaining. File hits download
  automatically and report local paths (that's the point — agents read
  them); 'search messages' accepts --download for message attachments.`,

	"workflow": `agent-slack workflow — discover and run workflows.

LIST     workflow list <channel> — bookmarked + featured workflows with
         Ft… trigger ids.
PREVIEW  workflow preview <Ft…> — metadata, no side effects.
GET      workflow get <Ft…|Wf…> — definition: form fields (name/title/
         required) + steps.
RUN      workflow run <Ft…> --channel <…>
         Without --field: trips the trigger. With --field "Title=value"
         (repeatable, case-insensitive titles): submits the workflow's form —
         requires browser auth (xoxc/xoxd) since it drives Slack's client
         APIs over a short-lived WebSocket.`,

	"later": `agent-slack later — saved-for-later (browser auth).

LIST     later list [--state in_progress|archived|completed|all]
         [--limit 20] [--counts-only] — items + {"@counts":…} meta line.
MUTATE   later save|complete|archive|reopen|remove <target> [--ts …]
REMIND   later remind <target> --in <30m|2d|tomorrow 5pm|next friday>`,

	"canvas": `agent-slack canvas — canvases as Markdown.

GET  canvas get <…/docs/… URL | F…> [--max-chars 20000]
     Downloads the canvas HTML export and converts it to Markdown.`,

	"file": `agent-slack file — point pulls.

DOWNLOAD  file download <F…> — fetch one file (by the id shown in message/
          search file metadata) to the cache dir; prints the local path.
          Canvas-mode files convert to Markdown.`,

	"api": `agent-slack api — raw escape hatch.

CALL  api call <method> [--params '<json>'|<file>|-] [--multipart]
      POSTs any Slack Web API method with stored credentials and prints the
      raw response. Prefer wrapped commands when they exist. --multipart for
      internal methods (saved.*) that ignore urlencoded params.`,

	"auth": `agent-slack auth — credentials (stored in the OS keychain where available).

SETUP   auth import-desktop — extract xoxc/xoxd from Slack Desktop (best).
        auth import-browser <name> — chrome, brave, firefox, zen, opera, safari
        auth parse-curl — paste a copied 'Copy as cURL' Slack request (stdin)
        auth add --workspace-url <url> (--token xoxb…|--xoxc … --xoxd …)
        auth add --workspace-url <url> --form — native OS dialog prompts the
          human for the secret; use this so tokens never appear in chat.
VERIFY  auth list (ls) — workspaces + where each secret is stored; flags
          secrets whose Keychain entry is gone. No secret material printed.
        auth test — calls Slack's auth.test with the resolved credentials.
MANAGE  auth set-default <url> | auth remove <url>
ENV     SLACK_TOKEN (+ SLACK_COOKIE_D + SLACK_WORKSPACE_URL for xoxc browser
          tokens) override the stored credentials for one invocation.
NOTE    expired browser tokens auto-refresh from Slack Desktop mid-command.`,
}
