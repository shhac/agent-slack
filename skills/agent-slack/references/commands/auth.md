# auth commands

Global flags and the `--yes` gate convention: see the [commands index](../commands.md).
In-binary version: `agent-slack auth usage`.

| Command | Notes |
|---|---|
| `auth list` (aliases `ls`, `whoami`) | configured workspaces + where each secret is stored (`keychain`/`file`/`missing`); no secret material printed |
| `auth test` | calls Slack `auth.test` with the resolved credentials |
| `auth import-desktop` | extract xoxc/xoxd from Slack Desktop (best); macOS/Linux/Windows |
| `auth import-browser <name>` | from a browser — `chrome`, `brave` (running tab, macOS); `firefox`, `zen` (profile on disk, `--profile <sel>`); `opera` (profile on disk); `safari` (running tab + cookie store, macOS, needs Full Disk Access) |
| `auth parse-curl` | read a "Copy as cURL" Slack request on stdin, import its xoxc/xoxd |
| `auth add --workspace-url <url> (--token … \| --xoxc … --xoxd …)` | add credentials directly |
| `auth add --workspace-url <url> --form` | prompt for missing secrets via a native OS dialog (keeps tokens out of chat) |
| `auth set-default <url>` / `auth remove <url>` | manage the default workspace and stored secrets |
