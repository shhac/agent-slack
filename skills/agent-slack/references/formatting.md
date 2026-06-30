# Message formatting

agent-slack speaks **standard Markdown** by default in both directions: text you
send is parsed as Markdown, and messages you read are rendered as Markdown.
`--slack-markdown` opts into Slack's native mrkdwn dialect instead (per command,
so outbound and inbound are independent).

## Markdown dialect (default)

| Feature       | Write this              | Notes                                   |
|---------------|-------------------------|-----------------------------------------|
| Bold          | `**bold**`              |                                         |
| Italic        | `*italic*` or `_italic_`|                                         |
| Bold + italic | `***both***`            |                                         |
| Strikethrough | `~~strike~~`            | a single `~` is literal (e.g. `~123`)   |
| Underline     | `__underline__`         | **extension** — `__` is underline, not bold |
| Inline code   | `` `code` ``            | contents are literal                    |
| Code block    | ```` ```\ncode\n``` ````| language hint after ``` is dropped      |
| Link          | `[label](https://x)`    | unlabeled `[url](url)` → inline link chip; bare URLs don't auto-link |
| Bulleted list | `- item` / `* item`     | indent two spaces for one sub-level     |
| Numbered list | `1. item` / `1) item`   |                                         |
| Blockquote    | `> quoted`              |                                         |
| Escape        | `\*literal\*`           | backslash before `* _ ~ \` [ ] ( ) @ :` |

Nesting works: `**bold with _italic_ and `code`**` styles each span correctly.

### Links

Prefer a **labeled link** — `[release notes](https://acme.com/releases/4.2)` —
whenever the link has a natural name. In `--slack-markdown` mode the equivalent
is `<https://…|label>`.

An **unlabeled link** — `[url](url)` (label same as the URL) or `<url>` in
mrkdwn — is upgraded on send to Slack's inline link **chip**: a pill showing the
scheme-stripped URL (`https://github.com/acme/widgets` → `github.com/acme/widgets`),
exactly what Slack's own composer produces when you paste a URL. This is the nice
rendering — reach for it when the URL itself is the thing you want to show. A
deliberately *labeled* link is always left as a plain link. A truly bare URL in
running text is **not** auto-linked, so wrap it in `[url](url)`/`<url>` to chip it.

A same-workspace **message permalink** in either unlabeled form (or bare in text)
becomes the richer inline message-reference chip instead — see
[commands/message.md](commands/message.md).

## Mentions

All of these resolve to real mentions at send time:

- `@here`, `@channel`, `@everyone` — broadcasts
- `@U05ABC…` or `<@U05ABC…>` — a user by id
- `@alice` — a **username handle** (resolved via the workspace, cached)
- `@marketing` — a **usergroup handle** → `<!subteam^S…>` (resolved + cached 24h)
- `#general` — a **channel name** → `<#C…>` (resolved + cached)

A bare `@name` is tried as a user first, then as a usergroup; if neither matches
it stays literal. Use an id (`<@U…>` / `<!subteam^S…>`) when you need to be exact.

### `#channel` vs Markdown headings

Slack has no Markdown headings, so there is no real conflict — and the two
shapes are distinguished structurally, not heuristically:

- **Channel** — `#name` with a name character flush against the `#`
  (`#general`, `word #ops`). Lowercase letters/digits/`-`/`_`, the way Slack
  stores channel names. Resolved to a link only when it matches a real channel;
  otherwise left literal.
- **Heading** — `# Title` (a space after the `#`) never matches: the space
  isn't a name character. `## Sub` and friends are safe for the same reason.
- **Not a channel** — `C#`/`F#` (the `#` follows a word character),
  already-formed `<#C…>` tokens, anything inside code spans/blocks, and
  all-digit refs like `#5`/`#1234` (issue/PR numbers) are all left untouched.

## --slack-markdown (opt-out)

Pass `--slack-markdown` to use Slack mrkdwn instead of standard Markdown:

- **Outbound** (`message send`/`edit`, `message draft create`/`edit`): text is
  read as Slack mrkdwn — `*bold*`, `_italic_`, `~strike~`, `<url|label>`.
- **Inbound** (`message get`/`list`, `search`, `unreads`, `later`): content is
  returned as native Slack mrkdwn rather than converted to Markdown.

Mention resolution still runs in `--slack-markdown` mode; the flag only changes
the *formatting* dialect.

## Notes

- `--blocks <file|->` and the raw `api` command bypass dialect conversion
  entirely (you provide Block Kit / params verbatim).
- When Markdown formatting is present, it is sent as rich_text blocks and the
  notification/`text` fallback is the marker-stripped plain text.
