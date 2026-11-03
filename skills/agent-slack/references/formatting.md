# Message formatting

agent-slack speaks **standard Markdown** by default in both directions: text you
send is parsed as Markdown, and messages you read are rendered as Markdown.
`--slack-markdown` opts into Slack's native mrkdwn dialect instead (per command,
so outbound and inbound are independent).

Two ways to compose a message:

- **Markdown text** (the default, and the right starting point). It covers
  everything Slack's own composer can make: emphasis, links, lists, quotes,
  code, headings, dividers — and pipe tables and checklists on top. Mentions
  and `#channels` resolve, links become chips, and the notification preview is
  built for you.
- **Raw Block Kit** (`--blocks`) for layouts Markdown has no syntax for —
  two-column fields, footer lines, colour swatches and whatever else Block Kit
  offers. See [Raw Block Kit](#raw-block-kit---blocks).

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
| Image         | `![alt](./chart.png)`   | on its own line; local file (uploaded), `https://` URL, or Slack file id `F…`. See [Images](#images) |
| Escape        | `\*literal\*`           | backslash before `* _ ~ \` [ ] ( ) @ : # - \| !` |

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

### Images

Put a Markdown image **on its own line** and it becomes an image block, placed
right there between the surrounding text, headings or tables:

```
## Latency this week

![p95 latency by service](./out/latency.png "Week 44")

Checkout is the outlier — details below.
```

The source can be:

- **A local file** — `./chart.png`, `/tmp/x.png`, `~/Desktop/x.png`, or
  `<./path with spaces.png>`. It's uploaded when you send and placed inline;
  posting shares it into that conversation only. Only real images go up (PNG,
  JPEG, GIF, WebP, BMP — checked by content, not extension); anything else is
  refused before upload, so send other files with `--attach`.
- **A public `https://` URL** — Slack fetches it itself, so it must be publicly
  reachable; a host that blocks Slack's fetcher fails the send with
  `invalid_blocks`.
- **A Slack file id** (`F…`) — any image Slack already holds, e.g. from
  `files[].id` on a message you read. This is also how images read back, so a
  read message re-sends unchanged.

Alt text is required by Slack — it's the notification text and what a
screen reader says; leave it empty and the file name is used. An optional
`"title"` becomes the caption above the image. An image mid-sentence stays
text; `\![…](…)` keeps one literal. Works on `message send`, `edit`, drafts and
`--forward` captions.

Escape to keep any of these literal: `\# not a heading`, `\---`, `a \| b`.
Structure applies in `--slack-markdown` mode too — it's line-level, not a
dialect question. Slack limits a message to 50 blocks; every heading, divider
and table is one, and so is each run of text between them. Past the limit Slack
rejects the send with an agent-fixable `invalid_blocks` error.

Reading returns the same syntax: headings as `#`/`##`/`###` (an app's title
header with no size reads as `#`), dividers as `---`, tables as pipe tables with
their alignment, checklists as `- [ ]`/`- [x]`, and images as
`![alt](F… "title")` (or the image's URL when Slack doesn't hold the file).

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

## Raw Block Kit (`--blocks`)

When the layout matters more than the prose — a status card, a summary with
labelled fields, a footer of metadata — write the blocks yourself. `--blocks`
takes a JSON array of [Block Kit](https://docs.slack.dev/reference/block-kit/blocks)
blocks from a file or `-` (stdin), on `message send` and
`message draft create`/`edit`.

```bash
agent-slack message send "#deploys" "Deploy: api is healthy" --blocks ./card.json
```

```json
[
  {"type": "header", "level": 2, "text": {"type": "plain_text", "text": "Deploy summary"}},
  {"type": "section",
   "text": {"type": "mrkdwn", "text": "*api* rolled out to production"},
   "fields": [
     {"type": "mrkdwn", "text": "*Owner*\n<@U0FAKE01>"},
     {"type": "mrkdwn", "text": "*Status*\nHealthy"},
     {"type": "mrkdwn", "text": "*Region*\neu-west-1"},
     {"type": "mrkdwn", "text": "*Duration*\n4m 12s"}
   ]},
  {"type": "divider"},
  {"type": "context", "elements": [
    {"type": "mrkdwn", "text": "Build 1234 · <https://ci.example.com/1234|logs>"}
  ]}
]
```

How it differs from Markdown text:

- **The blocks are sent verbatim** — no conversion, no mention resolution, no
  link chips. Write mentions as `<@U…>` / `<#C…>` / `<!subteam^S…>` (or
  `user`/`channel` elements in rich_text), and look ids up first
  (`user get @alice`).
- **`mrkdwn` text objects are Slack mrkdwn, not Markdown**: `*bold*`,
  `_italic_`, `~strike~`, `<url|label>`, `\n` for a line break.
- **The text argument becomes the notification/preview fallback** (the blocks
  replace its rendering). Always pass one — without it the notification has
  nothing to show.
- Prose inside a block-built message can still be rich: a `rich_text` block
  holds sections, lists, quotes and code exactly as Markdown would produce
  them. The quickest way to see the shape is to read a Markdown-sent message
  back with `message get <permalink> --full`.
- `message edit` is text-only. To change a raw-blocks message, call
  `agent-slack api call chat.update --params '{"channel":"C…","ts":"…","blocks":[…]}'`.
- `--blocks` combines with `--attach` (see [Attachments](#attachments)).

### Attachments

Files aren't blocks: a message carries its `blocks` and its `files` side by
side, and Slack draws the files after the blocks. So `--attach` works with
everything above — Markdown structure or `--blocks` — on `message send` and on
drafts:

```bash
agent-slack message send "#perf" "## Latency report
| Service | p95 |
| :--- | ---: |
| checkout | 412 |" --attach ./latency.csv
agent-slack message send "#deploys" "Deploy: api is healthy" --blocks ./card.json --attach ./graph.png
```

Slack previews common files inline under the message (a CSV as a table, an
image as a thumbnail). To place an image *between* blocks instead, use a
Markdown image on its own line — see [Images](#images). A `--schedule`d send
still can't carry `--attach` files.

**Not a closed list.** Any block or rich_text element Slack accepts on
`chat.postMessage` works — layout blocks (`section`, `context`, `header`,
`divider`, `table`, `rich_text`, …) and rich_text elements (`text`, `link`,
`user`, `channel`, `usergroup`, `broadcast`, `emoji`, `color`, …). Slack
validates on send: a bad block fails the whole message with `invalid_blocks`,
and the error hint carries Slack's reason and a JSON pointer to the offending
field (e.g. `must be less than 4 [json-pointer:/blocks/0/level]`) — fix that
field and resend.

What we've seen in practice (desktop client, Nov 2026) — useful, not
exhaustive:

| Renders well | Accepted, but caveat | Rejected |
|---|---|---|
| `section` text + two-column `fields`; `context` footer lines; `table`; `header` levels 1–3; `divider`; rich_text `color` (a swatch beside the hex); `image` by `slack_file.id` or public `image_url` | rich_text `date` shows only its `fallback`, not a localised time; checklists draw as plain bullets | `image_url` on a host Slack can't fetch |

Interactive elements (buttons, menus, inputs) belong to apps; a user token
posting them is untested here, and nothing would handle a click. A draft
holding blocks the composer can't edit (table, section, context) opens as
read-only cards in the composer and still sends intact.

**Checking the result:** `message get <permalink>` shows the text rendering a
reader's agent would see (sections and fields as lines, tables as pipe tables).
It can't show visual layout — when that matters, ask the user to look, or test
in their self-DM if they've asked you to.

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
  entirely (you provide Block Kit / params verbatim) — see
  [Raw Block Kit](#raw-block-kit---blocks).
- When Markdown formatting is present, it is sent as rich_text blocks and the
  notification/`text` fallback is the marker-stripped plain text (heading
  markers included).
- Sending with `--attach` keeps all of it: the rendered blocks ride on the
  upload, with the files drawn after them.
