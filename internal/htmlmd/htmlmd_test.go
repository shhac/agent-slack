package htmlmd

import (
	"strings"
	"testing"
)

func TestConvert(t *testing.T) {
	html := `<html><head><title>chrome</title></head><body>
<main>
<h1>Launch plan</h1>
<p>Step <strong>one</strong> and <em>two</em>.</p>
<ul><li>alpha</li><li>beta</li></ul>
<pre><code>make deploy</code></pre>
<a href="https://example.com">docs</a>
</main>
</body></html>`
	md := Convert(html)
	for _, want := range []string{"# Launch plan", "**one**", "- alpha", "make deploy", "[docs](https://example.com)"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
	if strings.Contains(md, "chrome") {
		t.Errorf("content outside <main> leaked: %s", md)
	}
}

func TestConvertPlainFragment(t *testing.T) {
	if md := Convert("<p>just text</p>"); !strings.Contains(md, "just text") {
		t.Errorf("md = %q", md)
	}
}
