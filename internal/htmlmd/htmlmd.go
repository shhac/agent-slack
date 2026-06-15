// Package htmlmd converts canvas-export HTML to Markdown. It wraps
// html-to-markdown the same way the TS original wrapped turndown: prefer the
// primary content node (main/article/body) and keep <br> line breaks.
package htmlmd

import (
	"regexp"
	"strings"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
)

// Convert renders HTML as Markdown. Conversion failures degrade to the raw
// text content rather than erroring — a partially readable canvas beats none.
func Convert(html string) string {
	content := extractPrimaryContent(html)
	md, err := htmltomarkdown.ConvertString(content)
	if err != nil {
		return strings.TrimSpace(stripTags(content))
	}
	return md
}

// primaryContentRes matches the preferred document containers in order: <main>,
// then <article>, then <body> — canvas exports wrap the document in chrome we
// don't want converted.
var primaryContentRes = []*regexp.Regexp{
	regexp.MustCompile(`(?is)<main\b[^>]*>(.*?)</main>`),
	regexp.MustCompile(`(?is)<article\b[^>]*>(.*?)</article>`),
	regexp.MustCompile(`(?is)<body\b[^>]*>(.*?)</body>`),
}

func extractPrimaryContent(html string) string {
	for _, re := range primaryContentRes {
		if m := re.FindStringSubmatch(html); m != nil {
			return m[1]
		}
	}
	return html
}

var tagRe = regexp.MustCompile(`(?s)<[^>]*>`)

func stripTags(html string) string {
	return tagRe.ReplaceAllString(html, "")
}
