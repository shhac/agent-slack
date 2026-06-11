package cli

import (
	"strings"
	"testing"
)

func TestCanvasGet(t *testing.T) {
	f := newCLIFixture(t)
	host := fileHost(t, "text/html", `<html><body><main><h1>Plan</h1><p>Step <strong>one</strong></p></main></body></html>`)
	f.server.HandleBody("files.info", map[string]any{
		"ok": true,
		"file": map[string]any{
			"id": "F08012345AB", "title": "Q3 Plan",
			"url_private_download": host.URL + "/canvas",
		},
	})

	out, _, err := f.run(t, "canvas", "get", "F08012345AB")
	if err != nil {
		t.Fatal(err)
	}
	canvas := parseJSON(t, out)["canvas"].(map[string]any)
	md := canvas["markdown"].(string)
	if !strings.Contains(md, "# Plan") || !strings.Contains(md, "**one**") {
		t.Errorf("markdown = %q", md)
	}
	if canvas["title"] != "Q3 Plan" {
		t.Errorf("canvas = %v", canvas)
	}
}
