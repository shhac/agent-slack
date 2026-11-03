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
| Bulleted list | `- item` / `* item`     | indent to nest; any depth, spaces or tabs |
| Numbered list | `1. item` / `1) item`   | first number sets the start; nesting doesn't restart it |
| Blockquote    | `> quoted`              |                                         |
| Heading       | `# Big` `## Medium` `### Small` | Slack's three sizes; `####`+ is small. Space after `#` required |
| Divider       | `---` (or `***`, `___`) | a line on its own; see [Structure](#structure) |
| Table         | `\| a \| b \|` + `\|---\|---\|` | GFM pipe table; the composer can't make these |
| Checklist     | `- [ ] todo` / `- [x] done` | works on bullets and numbers, nests like any list |
| Escape        | `\*literal\*`           | backslash before `* _ ~ \` [ ] ( ) @ : # - \|` |

Nesting works: `**bold with _italic_ and `code`**` styles each span correctly.

### Lists

Indent a list item to nest it, to any depth, mixing bullets and numbers freely:

```
1. First
    - a sub-bullet
    - another
2. Second
3. Third
```

That renders as `1, 2, 3` at the top level with the bullets indented beneath
item 1. Slack has no nested-list container — depth travels on each list block's
`indent`, and a numbered run interrupted by a sub-list resumes with `offset`, so
a sub-list never restarts the numbering above it.

Two- and four-space indents and tabs all work; what matters is that a nested
item is indented further than its parent. The first number sets where an
ordered list starts (`5.` begins at five), so lazy `1. 1. 1.` still counts up.

### Structure

Headings, dividers and tables are separate Block Kit blocks rather than inline
styles, so a message using them is sent as a sequence of blocks — exactly what
Slack's composer writes for its heading and divider buttons.

- **Headings** — `#`, `##`, `###` map to Slack's big/medium/small heading;
  deeper levels become small (Slack accepts only three). A heading is plain
  text: bold/italic inside it are dropped (it's already bold), and `:emoji:`
  works. A heading containing a **link or mention**, or longer than 150
  characters, can't be a Slack heading — it's sent as a **bold line** instead so
  nothing is lost.
- **Dividers** — `---`, `***` or `___` (spaces between allowed) alone on a line.
  A `---` directly under a paragraph is still a divider, not a setext heading.
- **Tables** — a header row, a delimiter row (`|---|`, with `:--`, `:-:`, `--:`
  for left/center/right), then body rows until a line without a `|`. Outer
  pipes are optional. Cells take inline formatting, links, mentions and emoji;
  write `\|` for a literal pipe. Short rows are padded and long rows cut to the
  header's width (Slack rejects ragged tables). Slack caps a table at 100 rows
  and 20 columns.
- **Checklists** — `[ ]`/`[x]` (or `[X]`) followed by a space, right after a list
  marker. Checked state is stored and round-trips through agent-slack, but
  Slack's client currently draws checklist items as plain bullets — readers
  can't see which are ticked, so say so in the text when it matters.

```
## Release 4.2

| Service  | p95 (ms) |
| :------- | -------: |
| checkout |      412 |

---

- [x] tests
- [ ] docs
```

Escape to keep any of these literal: `\# not a heading`, `\---`, `a \| b`.
Structure applies in `--slack-markdown` mode too — it's line-level, not a
dialect question. Slack limits a message to 50 blocks; every heading, divider
and table is one, and so is each run of text between them. Past the limit Slack
rejects the send with an agent-fixable `invalid_blocks` error.

Reading returns the same syntax: headings as `#`/`##`/`###` (an app's title
header with no size reads as `#`), dividers as `---`, tables as pipe tables with
their alignment, and checklists as `- [ ]`/`- [x]`.

### Beyond Markdown: raw blocks

Your token can also post blocks the composer has no button for — `section`
blocks with two-column `fields`, small grey `context` lines, and a rich_text
`color` element (`{"type":"color","value":"#FF5A1F"}`, drawn as a swatch).
There is no Markdown syntax for these: pass them verbatim with
`--blocks <file|->`. A rich_text `date` element is accepted but the client shows
only its `fallback` text, so don't rely on it for a localised time.

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

The two shapes are distinguished structurally, not heuristically:

- **Channel** — `#name` with a name character flush against the `#`
  (`#general`, `word #ops`). Lowercase letters/digits/`-`/`_`, the way Slack
  stores channel names. Resolved to a link only when it matches a real channel;
  otherwise left literal.
- **Heading** — `# Title` (a space after the `#`) is a heading, never a channel:
  the space isn't a name character. `## Sub` and friends likewise.
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
  notification/`text` fallback is the marker-stripped plain text (heading
  markers included).
- Sending with `--attach` posts plain text, so headings, tables and the rest
  degrade to their text. A draft with `--attach` keeps them.
