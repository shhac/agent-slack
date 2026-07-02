# Alias-keyed workspaces

Status: implemented (credentials file version 2).

## Problem

Version 1 keyed everything by normalized workspace URL:

- one `credentials.json` entry per URL, so **two humans in the same Slack
  workspace could not both hold credentials** — the second import clobbered
  the first;
- Keychain accounts were `xoxc:<url>` / `token:<url>`, inheriting the same
  collision;
- the browser `d` cookie lived in **one shared `xoxd` account** across all
  browser workspaces — a single-human assumption baked into the secret store.

The multi-user MCP direction (per-principal credential bindings, see
lib-agent-mcp) needs the unit of credential ownership to be "a named
credential set", not "a URL". Every sibling agent-* tool already works this
way (named profiles/orgs/projects with a default); v1 agent-slack was the
family outlier.

## Shape (version 2)

```json
{
  "version": 2,
  "default_workspace": "acme",
  "workspaces": [
    {
      "alias": "acme",
      "workspace_url": "https://acme.slack.com",
      "workspace_name": "Acme",
      "team_id": "T…", "user_id": "U…", "team_domain": "acme",
      "auth": { "auth_type": "browser", "xoxc_token": "__KEYCHAIN__", "xoxd_cookie": "__KEYCHAIN__" }
    }
  ]
}
```

- **`alias` is the unique key.** URL is metadata; several aliases may share a
  URL (that is the point).
- `default_workspace` holds an alias (v1: `default_workspace_url`).
- Keychain accounts are per-alias: `xoxc:<alias>`, `token:<alias>`,
  `xoxd:<alias>`. The shared `xoxd` account is gone; each browser workspace
  carries its own cookie copy. Redundant for one human with three teams,
  correct for three humans with one team.

## Alias rules

- Explicit via `auth add --alias`; otherwise derived: team domain, else URL
  host minus `.slack.com`, else workspace name (lowercased, non-alphanumerics
  collapsed to `-`). Collisions uniquify with `-2`, `-3`, ….
- Imports (`import-desktop`, `import-browser`, `parse-curl`) carry no alias:
  a team whose URL matches exactly one stored entry updates that entry
  (keeping its alias); zero matches creates a derived alias; **multiple
  matches is a structured error** telling the caller to use
  `auth add --alias` — guessing which human's entry to overwrite would be a
  cross-user credential write.
- The Slack-Desktop self-heal refresh always targets the alias it was
  constructed for, never a URL match.

## Selector semantics

`--workspace` (and `auth set-default` / `auth remove` arguments) resolve as:
exact alias match first, then exact normalized URL (unique per URL only),
then the v1 fuzzy forms (substring of URL, host, host minus `.slack.com`,
name, team domain — now also alias). Ambiguity errors list aliases, e.g.
`alice (https://acme.slack.com)`.

## Migration (v1 → v2)

One-shot, performed under the cross-process file lock the first time the
store loads a version-1 file:

1. every workspace gets a derived alias;
2. `default_workspace_url` maps to the alias of its first URL match;
3. secrets move to per-alias Keychain accounts (`xoxc:<url>` → `xoxc:<alias>`,
   shared `xoxd` → `xoxd:<alias>` for every browser workspace) and old
   accounts are deleted — the shared `xoxd` last, after every workspace has
   its copy;
4. the file is rewritten as version 2.

A Keychain read that fails during migration leaves the placeholder intact —
the workspace then reports its secret as `missing` in `auth list`, same as a
dangling v1 placeholder, and heals via the usual re-import paths.

## Fail-closed mode

`AGENT_SLACK_REQUIRE_IDENTITY=1` disables every implicit credential source:
an invocation with no explicit `--workspace` selector returns a structured
`fixable_by: agent` error before the default workspace or `SLACK_TOKEN` env
can serve it. A multi-user MCP runner sets this on every subprocess so a bug
in its identity-binding plumbing fails loudly instead of silently acting as
the operator's default identity.

## Not in scope

Aliases are still a flat namespace in one OS user's store: this is the
per-credential-set boundary the MCP binding layer selects among
(`--workspace <alias>`), not a security boundary between principals. Guarding
"which MCP principal may use which alias" is lib-agent-mcp's binding store's
job, not the credential file's. That wiring exists: `mcpIdentityBinding`
(internal/cli/mcp_binding.go) translates a principal's `workspace` binding
into `--workspace <alias>` + `AGENT_SLACK_REQUIRE_IDENTITY=1` on every
principal-authenticated tool call — pair a principal with
`agent-slack mcp pair add <name> --bind workspace=<alias>`. See
lib-agent-mcp's design-docs/multi-user.md for the trust model.
