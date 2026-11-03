package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// structureSource carries every standalone structure the composer can write
// (and a table, which it can't): each must reach the wire as its own block.
const structureSource = "# Release\n\nShipped.\n\n---\n\n- [x] tests\n\n| svc | p95 |\n|---|--:|\n| api | 12 |"

var structureBlockTypes = []string{"header", "rich_text", "divider", "rich_text", "table"}

func wireBlockTypes(t *testing.T, raw string) []string {
	t.Helper()
	var blocks []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
		t.Fatalf("unmarshal blocks %q: %v", raw, err)
	}
	types := make([]string, len(blocks))
	for i, b := range blocks {
		types[i] = b.Type
	}
	return types
}

func assertBlockTypes(t *testing.T, path string, raw string) {
	t.Helper()
	got := wireBlockTypes(t, raw)
	if len(got) != len(structureBlockTypes) {
		t.Fatalf("%s: block types %v, want %v", path, got, structureBlockTypes)
	}
	for i := range got {
		if got[i] != structureBlockTypes[i] {
			t.Errorf("%s: block %d = %s, want %s", path, i, got[i], structureBlockTypes[i])
		}
	}
}

func TestStructureReachesTheWire(t *testing.T) {
	t.Run("send", func(t *testing.T) {
		f := newCLIFixture(t)
		f.resolvableChannel("C123")
		f.server.HandleBody("chat.postMessage", map[string]any{"ok": true, "ts": "1.000001", "channel": "C123"})
		if _, _, err := f.run(t, "message", "send", "#general", structureSource); err != nil {
			t.Fatal(err)
		}
		call := f.server.CallsFor("chat.postMessage")[0]
		assertBlockTypes(t, "send", call.Params.Get("blocks"))
		if got := call.Params.Get("text"); !strings.HasPrefix(got, "Release\n") {
			t.Errorf("text fallback should drop heading markers: %q", got)
		}
	})

	t.Run("edit", func(t *testing.T) {
		f := newCLIFixture(t)
		f.server.HandleBody("chat.update", map[string]any{"ok": true})
		if _, _, err := f.run(t, "message", "edit", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379", structureSource, "--yes"); err != nil {
			t.Fatal(err)
		}
		assertBlockTypes(t, "edit", f.server.CallsFor("chat.update")[0].Params.Get("blocks"))
	})

	t.Run("draft", func(t *testing.T) {
		f := newBrowserCLIFixture(t)
		f.server.HandleBody("drafts.create", map[string]any{"ok": true, "draft": map[string]any{
			"id": "Dr0DRAFT", "destinations": []any{map[string]any{"channel_id": "C12345678"}}}})
		if _, _, err := f.run(t, "message", "draft", "create", "C12345678", structureSource); err != nil {
			t.Fatal(err)
		}
		assertBlockTypes(t, "draft", f.server.CallsFor("drafts.create")[0].Params.Get("blocks"))
	})
}

// Rich text on an attachment send rides as completeUploadExternal blocks — the
// public upload API takes them — instead of degrading to a plain comment.
// Slack ignores blocks whenever initial_comment is present, so only blocks go.
func TestAttachSendCarriesBlocks(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"markdown structure", []string{structureSource}},
		{"--blocks file", []string{"fallback", "--blocks", "BLOCKS"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCLIFixture(t)
			uploadHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(uploadHost.Close)
			f.resolvableChannel("C12345678")
			f.server.HandleBody("files.getUploadURLExternal", map[string]any{
				"ok": true, "upload_url": uploadHost.URL + "/upload", "file_id": "F0UPLOAD1",
			})
			f.server.HandleBody("files.completeUploadExternal", map[string]any{"ok": true})

			dir := t.TempDir()
			attachment := filepath.Join(dir, "latency.csv")
			blocksFile := filepath.Join(dir, "blocks.json")
			if err := os.WriteFile(attachment, []byte("a,b\n1,2\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(blocksFile, []byte(`[{"type":"divider"},{"type":"context","elements":[{"type":"mrkdwn","text":"x"}]}]`), 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"message", "send", "#general"}
			for _, a := range tc.args {
				if a == "BLOCKS" {
					a = blocksFile
				}
				args = append(args, a)
			}
			if _, _, err := f.run(t, append(args, "--attach", attachment)...); err != nil {
				t.Fatal(err)
			}

			complete := f.server.CallsFor("files.completeUploadExternal")[0]
			if complete.Params.Has("initial_comment") {
				t.Errorf("initial_comment would make Slack drop the blocks: %q", complete.Params.Get("initial_comment"))
			}
			if !complete.Params.Has("blocks") {
				t.Fatalf("no blocks on the upload: %v", complete.Params)
			}
			if tc.name == "markdown structure" {
				assertBlockTypes(t, "attach", complete.Params.Get("blocks"))
			}
		})
	}
}
