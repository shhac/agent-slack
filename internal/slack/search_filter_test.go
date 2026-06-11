package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/render"
)

func TestPassesContentTypeFilter(t *testing.T) {
	noFiles := render.CompactMessage{Content: "text only"}
	withImage := render.CompactMessage{Files: []render.CompactFile{{Mimetype: "image/png"}}}
	withSnippet := render.CompactMessage{Files: []render.CompactFile{{Mode: "snippet", Mimetype: "text/plain"}}}
	mixed := render.CompactMessage{Files: []render.CompactFile{{Mimetype: "application/pdf"}, {Mimetype: "image/jpeg"}}}

	cases := []struct {
		name string
		m    render.CompactMessage
		ct   ContentType
		want bool
	}{
		{"any always passes", noFiles, "any", true},
		{"text wants no files", noFiles, "text", true},
		{"text rejects files", withImage, "text", false},
		{"file wants files", withImage, "file", true},
		{"file rejects no files", noFiles, "file", false},
		{"image matches mimetype", withImage, "image", true},
		{"image in mixed set", mixed, "image", true},
		{"image rejects snippet", withSnippet, "image", false},
		{"snippet matches mode", withSnippet, "snippet", true},
		{"snippet rejects image", withImage, "snippet", false},
	}
	for _, tc := range cases {
		if got := PassesContentTypeFilter(tc.m, tc.ct); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPassesFileContentTypeFilter(t *testing.T) {
	cases := []struct {
		mode, mimetype string
		ct             ContentType
		want           bool
	}{
		{"", "image/PNG", "image", true}, // case-insensitive mimetype
		{"snippet", "text/plain", "snippet", true},
		{"hosted", "text/plain", "snippet", false},
		{"", "text/plain", "text", true},
		{"", "text/markdown", "text", false}, // file-text means exactly text/plain
		{"", "anything", "any", true},
		{"", "anything", "file", true},
	}
	for _, tc := range cases {
		if got := passesFileContentTypeFilter(tc.mode, tc.mimetype, tc.ct); got != tc.want {
			t.Errorf("(%q,%q,%q): got %v, want %v", tc.mode, tc.mimetype, tc.ct, got, tc.want)
		}
	}
}

// failingHost answers every request with the given status.
func failingHost(t *testing.T, status int, contentType, body string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestDownloadMessageFilesFailureSidecar(t *testing.T) {
	host := failingHost(t, http.StatusForbidden, "text/plain", "denied")
	c := New(Auth{Type: AuthStandard, Token: "xoxb-x"})
	dir := t.TempDir()

	msg := render.MessageSummary{Files: []render.FileSummary{{
		ID: "F0FAIL", Name: "secret.png", Mimetype: "image/png",
		URLPrivateDownload: host.URL + "/f",
	}}}
	results := DownloadMessageFiles(context.Background(), c, []render.MessageSummary{msg},
		MessageDownloads{DestDir: dir})

	res := results["F0FAIL"]
	if res.OK || !strings.Contains(res.Error, "403") {
		t.Fatalf("result = %+v", res)
	}
	// The path points at a readable sidecar describing the failure.
	if filepath.Base(res.Path) != "F0FAIL.download-error.txt" {
		t.Errorf("sidecar path = %q", res.Path)
	}
	content, err := os.ReadFile(res.Path)
	if err != nil || !strings.Contains(string(content), "403") {
		t.Errorf("sidecar content = %q, err %v", content, err)
	}
}

func TestDownloadCanvasLoginPageDetected(t *testing.T) {
	host := failingHost(t, http.StatusOK, "text/html", `<html><title>Sign in to Slack</title><body>login</body></html>`)
	c := New(Auth{Type: AuthBrowser, XOXC: "x", XOXD: "y", WorkspaceURL: "https://acme.slack.com"})
	dir := t.TempDir()

	msg := render.MessageSummary{Files: []render.FileSummary{{
		ID: "F0CANVAS", Mode: "canvas", URLPrivate: host.URL + "/c",
	}}}
	results := DownloadMessageFiles(context.Background(), c, []render.MessageSummary{msg},
		MessageDownloads{DestDir: dir, CanvasMarkdown: func(string) string { return "should not run" }})

	res := results["F0CANVAS"]
	if res.OK || !strings.Contains(res.Error, "auth/login page") {
		t.Fatalf("result = %+v (login pages must not convert to markdown)", res)
	}
}

func TestMapNetworkErrorContract(t *testing.T) {
	// A connection to a closed port maps to a retry-fixable error.
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := closed.URL
	closed.Close()

	c := New(Auth{Type: AuthBrowser, XOXC: "x", XOXD: "y", WorkspaceURL: url})
	_, err := c.API(context.Background(), "auth.test", nil)
	if err == nil {
		t.Fatal("expected network error")
	}
	if !strings.Contains(err.Error(), "network error calling auth.test") {
		t.Errorf("err = %v", err)
	}
}
