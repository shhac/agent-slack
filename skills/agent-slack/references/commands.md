# Commands (reference index)

The full command map, split one file per domain — read only the domain you
need. For the in-binary version of any domain, run `agent-slack <domain> usage`.

**Global flags** work on every command: `--workspace/-w <substring>`,
`--format/-f json|yaml|jsonl`, `--timeout/-t <ms>`, `--debug/-d`, `--full`.

**Gate (`--yes`):** a destructive command refuses to run without `--yes`;
instead it returns a description of what *would* happen (`fixable_by: human`).
Show that to the user, then re-run with `--yes`. Gated commands: `message
edit|delete`, `message draft delete`, `message scheduled cancel`, `channel
new|invite`, `emoji add|remove`.

| Domain | Commands | Reference |
|---|---|---|
| auth | `list` / `test` / `import-desktop` / `import-browser` / `parse-curl` / `add` / `set-default` / `remove` | [commands/auth.md](commands/auth.md) |
| message | `get` / `list` / `send` / `edit`\* / `delete`\* / `react` / `draft` / `scheduled` | [commands/message.md](commands/message.md) |
| channel | `list` / `get` / `members` / `new`\* / `invite`\* / `mark` | [commands/channel.md](commands/channel.md) |
| user | `list` / `get` / `dm-open` | [commands/user.md](commands/user.md) |
| usergroup | `list` / `get` / `members` | [commands/usergroup.md](commands/usergroup.md) |
| emoji | `list` / `get` / `search` / `add`\* / `remove`\* | [commands/emoji.md](commands/emoji.md) |
| search | `all` / `messages` / `files` | [commands/search.md](commands/search.md) |
| workflow | `list` / `preview` / `get` / `run` | [commands/workflow.md](commands/workflow.md) |
| unreads · later · canvas · file · api | reading commands + the raw escape hatch | [commands/other.md](commands/other.md) |
| cache · config | inspect / warm / purge the cache; persist TTLs | [commands/cache-config.md](commands/cache-config.md) |

\* = destructive (needs `--yes`).

Cross-cutting detail lives alongside this file: [targets.md](targets.md)
(targeting + multi-workspace), [formatting.md](formatting.md) (Markdown,
mentions, `--slack-markdown`), [output.md](output.md) (NDJSON/meta contract,
`--full`, payload shapes, the resolution cache).
