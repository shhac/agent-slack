package render

// Block is one top-level Block Kit block on an outbound message. Text becomes
// rich_text for prose, lists, code and quotes; headings, dividers and tables
// are blocks of their own, so one message is a sequence of these.
type Block interface{ outboundBlock() }

// RichTextBlock is a top-level rich_text block — and the cell type of a table.
type RichTextBlock struct {
	Type     string            `json:"type"`
	Elements []RichTextElement `json:"elements"`
}

// HeaderBlock is a heading as Slack's composer writes it: a header block whose
// level (1–3; Slack rejects anything else) picks big, medium or small.
type HeaderBlock struct {
	Type  string    `json:"type"`
	Level int       `json:"level"`
	Text  PlainText `json:"text"`
}

type PlainText struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Emoji bool   `json:"emoji"`
}

type DividerBlock struct {
	Type string `json:"type"`
}

// TableBlock is a Block Kit table. Every row must have the same number of cells
// (Slack rejects ragged rows), and column_settings entries must be objects —
// an unaligned column is {} rather than null.
type TableBlock struct {
	Type           string            `json:"type"`
	ColumnSettings []ColumnSetting   `json:"column_settings,omitempty"`
	Rows           [][]RichTextBlock `json:"rows"`
}

type ColumnSetting struct {
	Align string `json:"align,omitempty"`
}

func (RichTextBlock) outboundBlock() {}
func (HeaderBlock) outboundBlock()   {}
func (DividerBlock) outboundBlock()  {}
func (TableBlock) outboundBlock()    {}

func newRichTextBlock(elements []RichTextElement) RichTextBlock {
	return RichTextBlock{Type: "rich_text", Elements: elements}
}

func newHeaderBlock(level int, text string) HeaderBlock {
	return HeaderBlock{
		Type:  "header",
		Level: level,
		Text:  PlainText{Type: "plain_text", Text: text, Emoji: true},
	}
}

func newDividerBlock() DividerBlock { return DividerBlock{Type: "divider"} }

// blockBuilder accumulates rich_text elements until a standalone block
// interrupts them, so prose either side of a heading or table lands in its own
// rich_text block and the message keeps its source order.
type blockBuilder struct {
	blocks   []Block
	elements []RichTextElement
}

func (b *blockBuilder) addElement(el RichTextElement) {
	b.elements = append(b.elements, el)
}

func (b *blockBuilder) addBlock(block Block) {
	b.flush()
	b.blocks = append(b.blocks, block)
}

func (b *blockBuilder) flush() {
	if len(b.elements) == 0 {
		return
	}
	b.blocks = append(b.blocks, newRichTextBlock(b.elements))
	b.elements = nil
}

func (b *blockBuilder) build() []Block {
	b.flush()
	return b.blocks
}
