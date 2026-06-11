package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shhac/agent-slack/internal/output"
)

const usageText = `agent-slack: Slack CLI for AI agents. JSON in, JSON out, no interactivity.

COMMANDS
  auth       whoami | test | add | set-default | remove | import-desktop |
             import-chrome | import-brave | import-firefox | parse-curl
  message    get | list | send | edit* | delete* | react add/remove |
             scheduled list/cancel*
  channel    list | new* | invite* | mark
  user       list | get | dm-open
  search     all | messages | files
  workflow   list | preview | get | run
  canvas     get
  unreads    (top-level) unread messages across channels/DMs/threads
  later      list | save | complete | archive | reopen | remove | remind
  file       download <F…> — point-pull a file seen in any output
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
  'file download'. --resolve-users expands U… ids (cached 24h;
  --refresh-users busts the cache). --include-reactions opts into reactions.

ERRORS
  JSON on stderr: {"error","fixable_by","hint"}. fixable_by=agent → fix the
  input and retry; human → credentials/permissions need a person;
  retry → wait and re-run.

AUTH
  Stored per workspace (macOS Keychain). Setup: 'auth import-desktop' (or
  import-chrome/brave/firefox, parse-curl, add). Env override: SLACK_TOKEN
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
       --resolve-users, --refresh-users.
LIST   message list <target>
       Channel target → recent history (--limit 25 max 200, --oldest,
       --latest), chronological NDJSON + {"@channel_id":…} meta line.
       Thread permalink or --thread-ts/--ts → the whole thread (rows drop
       channel_id/thread_ts; they're in meta lines). Reaction filters:
       --with-reaction/--without-reaction (repeatable, need --oldest).
       Files are metadata-only unless --download.
SEND   message send <target> [text] [--thread-ts …] [--reply-broadcast]
       Targets: #channel, C…, U… (DM auto-opens), or a permalink (replies in
       that thread). Text is auto-formatted: @U… → mention, & < > escaped,
       bullet/numbered lists → rich_text. --attach <path> (repeatable),
       --blocks <file|-> raw Block Kit, --schedule <iso8601-with-tz|unix>,
       --schedule-in <30m|2d|tomorrow 9am>. Output includes ts + permalink.
EDIT   message edit <target> <text> --yes     (destructive)
DELETE message delete <target> --yes          (destructive)
REACT  message react add|remove <target> <emoji>   (:rocket:, rocket, or 🚀)
SCHED  message scheduled list [--channel …] [--cursor …]
       message scheduled cancel <id> --channel <…> --yes   (destructive)`,

	"channel": `agent-slack channel — conversations.

LIST   channel list [--user U…|@handle] [--all] [--limit 100] [--cursor …]
       Default: the authed user's conversations. Compact rows: id, name,
       is_private/is_im/is_mpim, is_member, num_members, topic; --full = raw.
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
GET      user get <U…|@handle|email>
DM-OPEN  user dm-open <users…> — open a DM or group DM (max 8); returns
         dm_channel_id to send into.`,

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
REMIND   later remind <target> --in <30m|2d|tomorrow|monday>`,

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

	"auth": `agent-slack auth — credentials (stored in the macOS Keychain).

SETUP   auth import-desktop — extract xoxc/xoxd from Slack Desktop (best).
        auth import-chrome | import-brave | import-firefox
        auth parse-curl — paste a copied 'Copy as cURL' Slack request (stdin)
        auth add --workspace-url <url> (--token xoxb…|--xoxc … --xoxd …)
VERIFY  auth whoami — configured workspaces (secrets never printed).
        auth test — calls Slack's auth.test with the resolved credentials.
MANAGE  auth set-default <url> | auth remove <url>
NOTE    expired browser tokens auto-refresh from Slack Desktop mid-command.`,
}

func registerUsage(parent *cobra.Command) {
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "LLM-optimized usage overview",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprint(output.Stdout(), usageText)
			return err
		},
	}
	parent.AddCommand(cmd)
}

// attachDomainUsage adds a `usage` subcommand to each domain that has a
// detail page. Called after all domains are registered.
func attachDomainUsage(root *cobra.Command) {
	for _, sub := range root.Commands() {
		text, ok := domainUsage[sub.Name()]
		if !ok {
			continue
		}
		sub.AddCommand(&cobra.Command{
			Use:   "usage",
			Short: "Detailed " + sub.Name() + " documentation for LLMs",
			RunE: func(cmd *cobra.Command, args []string) error {
				_, err := fmt.Fprintln(output.Stdout(), text)
				return err
			},
		})
	}
}
