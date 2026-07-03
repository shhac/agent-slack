# auth commands

Global flags and the `--yes` gate convention: see the [commands index](../commands.md).
In-binary version: `agent-slack auth usage`.

| Command | Notes |
|---|---|
| `auth list` (aliases `ls`, `whoami`) | configured credential sets (each a unique `alias` + workspace URL) + where each secret is stored (`keychain`/`file`/`missing`); no secret material printed |
| `auth test` | calls Slack `auth.test` with the resolved credentials |
| `auth import-desktop` | extract xoxc/xoxd from Slack Desktop (best); macOS/Linux/Windows |
| `auth import-browser <name>` | from a browser — `chrome`, `brave` (running tab, macOS); `firefox`, `zen` (profile on disk, `--profile <sel>`); `opera` (profile on disk); `safari` (running tab + cookie store, macOS, needs Full Disk Access) |
| `auth parse-curl` | read a "Copy as cURL" Slack request on stdin, import its xoxc/xoxd |
| `auth add [--alias <alias>] --workspace-url <url> (--token … \| --xoxc … --xoxd …)` | add credentials directly; `--alias` names the credential set (derived from the workspace when omitted) — several aliases may hold the same workspace URL (e.g. two humans in one Slack) |
| `auth add --workspace-url <url> --form` | prompt for missing secrets via a native OS dialog (keeps tokens out of chat) |
| `auth add --workspace-url <url> --stdin` | read secrets as one JSON object on stdin (`{"token": …}` or `{"xoxc": …, "xoxd": …}`) — machine path for scripts/enrollment; nothing in argv or process env |
| `auth set-default <alias>` / `auth remove <alias>` | manage the default credential set and stored secrets (both accept any `--workspace` selector); `auth remove` also clears that workspace's identity cache subtree (resolution cache + downloads) |

Imports carry no alias: each imported team updates the entry whose URL it
matches, or creates one under a derived alias. If several aliases share the
URL the import fails with a `fixable_by: agent` error — re-run
`auth add --alias <alias>` to say which credential set to update.

Env: `SLACK_TOKEN` (+ `SLACK_COOKIE_D` + `SLACK_WORKSPACE_URL`) override the
store for one invocation. `AGENT_SLACK_REQUIRE_IDENTITY=1` makes every
invocation without an explicit `--workspace` fail with a `fixable_by: agent`
error (no default-workspace or env fallback) — the fail-closed mode multi-user
MCP runners set alongside a per-principal `--workspace <alias>`.

Workspace entries may also appear via MCP browser enrollment: a named MCP
principal minted without a binding pastes their own token on the OAuth
approval page; it is verified via `auth.test` and stored under alias =
principal name. Such aliases are managed like any other (`auth list`,
`auth remove`).
