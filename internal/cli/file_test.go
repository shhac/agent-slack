package cli

import (
	"os"
	"strings"
	"testing"
)

func TestFileDownload(t *testing.T) {
	f := newCLIFixture(t)
	host := fileHost(t, "image/png", "PNGBYTES")
	f.server.HandleBody("files.info", map[string]any{
		"ok": true,
		"file": map[string]any{
			"id": "F123ABC45", "name": "diagram.png", "mimetype": "image/png",
			"url_private_download": host.URL + "/f",
		},
	})

	out, _, err := f.run(t, "file", "download", "F123ABC45")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	path := payload["path"].(string)
	if !strings.HasSuffix(path, "F123ABC45.png") {
		t.Errorf("path = %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "PNGBYTES" {
		t.Errorf("file content = %q, err %v", data, err)
	}
}
