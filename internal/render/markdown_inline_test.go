package render

import "testing"

func TestParseMarkdownInline(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain", "Hello world",
			`[{"type":"text","text":"Hello world"}]`},
		{"bold double asterisk", "a **bold** b",
			`[{"type":"text","text":"a "},{"type":"text","text":"bold","style":{"bold":true}},{"type":"text","text":" b"}]`},
		{"italic single asterisk", "an *italic* word",
			`[{"type":"text","text":"an "},{"type":"text","text":"italic","style":{"italic":true}},{"type":"text","text":" word"}]`},
		{"italic underscore", "an _italic_ word",
			`[{"type":"text","text":"an "},{"type":"text","text":"italic","style":{"italic":true}},{"type":"text","text":" word"}]`},
		{"bold italic triple", "***wow***",
			`[{"type":"text","text":"wow","style":{"bold":true,"italic":true}}]`},
		{"strike double tilde", "~~gone~~",
			`[{"type":"text","text":"gone","style":{"strike":true}}]`},
		{"underline double underscore", "__under__",
			`[{"type":"text","text":"under","style":{"underline":true}}]`},
		{"inline code", "run `npm i` now",
			`[{"type":"text","text":"run "},{"type":"text","text":"npm i","style":{"code":true}},{"type":"text","text":" now"}]`},
		{"markdown link", "see [docs](https://x.com) here",
			`[{"type":"text","text":"see "},{"type":"link","url":"https://x.com","text":"docs"},{"type":"text","text":" here"}]`},
		{"markdown link bare label", "[https://x.com](https://x.com)",
			`[{"type":"link","url":"https://x.com"}]`},
		{"nesting bold containing italic", "**bold _and italic_**",
			`[{"type":"text","text":"bold ","style":{"bold":true}},{"type":"text","text":"and italic","style":{"bold":true,"italic":true}}]`},
		{"single tilde stays literal", "~123 and ~456",
			`[{"type":"text","text":"~123 and ~456"}]`},
		{"single tilde pair stays literal", "about ~foo~ ok",
			`[{"type":"text","text":"about ~foo~ ok"}]`},
		{"intraword underscore stays literal", "file_name_here",
			`[{"type":"text","text":"file_name_here"}]`},
		{"unclosed bold stays literal", "a **bold start",
			`[{"type":"text","text":"a **bold start"}]`},
		{"escaped asterisks literal", "a \\*literal\\* b",
			`[{"type":"text","text":"a *literal* b"}]`},
		{"escaped double underscore literal", "\\_\\_notunder\\_\\_",
			`[{"type":"text","text":"__notunder__"}]`},
		{"escaped delimiter is not a closer", "a stray ** and an escaped \\*literal\\*",
			`[{"type":"text","text":"a stray ** and an escaped *literal*"}]`},
		{"escaped closer inside emphasis", "*a\\*b*",
			`[{"type":"text","text":"a*b","style":{"italic":true}}]`},
		{"user token passthrough", "hi <@U123456A>",
			`[{"type":"text","text":"hi "},{"type":"user","user_id":"U123456A"}]`},
		{"bare user mention", "@U05BRPTKL6A heads up",
			`[{"type":"user","user_id":"U05BRPTKL6A"},{"type":"text","text":" heads up"}]`},
		{"usergroup token passthrough", "ping <!subteam^S12345678|@team>",
			`[{"type":"text","text":"ping "},{"type":"usergroup","usergroup_id":"S12345678"}]`},
		{"broadcast bare", "cc @here now",
			`[{"type":"text","text":"cc "},{"type":"broadcast","range":"here"},{"type":"text","text":" now"}]`},
		{"emoji shortcode", "ship :rocket: it",
			`[{"type":"text","text":"ship "},{"type":"emoji","name":"rocket"},{"type":"text","text":" it"}]`},
		{"mixed run", "see **bold** and `code` and [x](https://y.com)",
			`[{"type":"text","text":"see "},{"type":"text","text":"bold","style":{"bold":true}},{"type":"text","text":" and "},{"type":"text","text":"code","style":{"code":true}},{"type":"text","text":" and "},{"type":"link","url":"https://y.com","text":"x"}]`},
		{"code span protects markers", "`a*b*c`",
			`[{"type":"text","text":"a*b*c","style":{"code":true}}]`},
		{"empty", "",
			`[{"type":"text"}]`},
	}
	for _, tc := range cases {
		jsonEqual(t, tc.name, ParseMarkdownInline(tc.input), tc.want)
	}
}

func TestPlainTextFromMarkdown(t *testing.T) {
	cases := []struct{ in, want string }{
		{"**bold** and _italic_ and `code`", "bold and italic and code"},
		{"see [the docs](https://x.com)", "see the docs"},
		{"ping @here and <@U12345678>", "ping <!here> and <@U12345678>"},
		{"line one\n- **item**", "line one\n- item"},
		{"~123 and file_name_here", "~123 and file_name_here"},
	}
	for _, tc := range cases {
		if got := PlainTextFromMarkdown(tc.in); got != tc.want {
			t.Errorf("PlainTextFromMarkdown(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
