# search commands

Global flags and the `--yes` gate convention: see the [commands index](../commands.md).
In-binary version: `agent-slack search usage`.

```
search messages <query>   # message hits
search files <query>      # file hits (auto-downloaded; local paths returned)
search all <query>        # both
```

Flags: `--channel` (repeatable), `--user`, `--after YYYY-MM-DD`,
`--before YYYY-MM-DD`, `--content-type any|text|image|snippet|file`,
`--limit` (20), `--max-content-chars` (4000), `--slack-markdown`, and
`--resolve none|cached|auto|fresh` (default `auto`; resolves referenced
users/channels/usergroups in hits, like `message get`).
