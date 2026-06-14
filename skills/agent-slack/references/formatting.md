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
| Link          | `[label](https://x)`    | bare URLs auto-link in Slack            |
| Bulleted list | `- item` / `* item`     | indent two spaces for one sub-level     |
| Numbered list | `1. item` / `1) item`   |                                         |
| Blockquote    | `> quoted`              |                                         |
| Escape        | `\*literal\*`           | backslash before `* _ ~ \` [ ] ( ) @ :` |

Nesting works: `**bold with _italic_ and `code`**` styles each span correctly.

## Mentions

All of these resolve to real mentions at send time:

- `@here`, `@channel`, `@everyone` — broadcasts
- `@U05ABC…` or `<@U05ABC…>` — a user by id
- `@alice` — a **username handle** (resolved via the workspace, cached)
- `@marketing` — a **usergroup handle** → `<!subteam^S…>` (resolved + cached 24h)

A bare `@name` is tried as a user first, then as a usergroup; if neither matches
it stays literal. Use an id (`<@U…>` / `<!subteam^S…>`) when you need to be exact.

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
