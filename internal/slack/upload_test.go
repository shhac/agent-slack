package slack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/mockslack"
)

// A file over Slack's 100MB limit is rejected locally — before any
// files.getUploadURLExternal call — with an agent-fixable error. Uses a sparse
// file (Truncate reports the size without allocating disk) so the test stays
// cheap.
func TestUploadDraftFilesRejectsTooLarge(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("files.getUploadURLExternal", map[string]any{"ok": true, "upload_url": "x", "file_id": "F1"})
	c := newStandardClient(t, server)

	path := filepath.Join(t.TempDir(), "huge.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxUploadBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()

	_, err = c.UploadDraftFiles(context.Background(), []string{path})
	if err == nil {
		t.Fatal("expected an error for an oversized file")
	}
	if !strings.Contains(err.Error(), "file too large") {
		t.Errorf("error = %v, want 'file too large'", err)
	}
	var aerr *agenterrors.APIError
	if !agenterrors.As(err, &aerr) || aerr.FixableBy != agenterrors.FixableByAgent {
		t.Errorf("want agent-fixable APIError, got %v", err)
	}
	if n := len(server.CallsFor("files.getUploadURLExternal")); n != 0 {
		t.Errorf("oversized file must be rejected before requesting an upload URL, got %d calls", n)
	}
}
