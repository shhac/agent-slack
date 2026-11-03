package slack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/mockslack"
)

// A file over Slack's 100MB limit is rejected locally — before any
// files.getUploadURLExternal call — with an agent-fixable error. Uses a sparse
// file (Truncate reports the size without allocating disk) so the test stays
// cheap.
func TestUploadUnsharedFilesRejectsTooLarge(t *testing.T) {
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

	_, err = c.UploadUnsharedFiles(context.Background(), []string{path})
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

// Slack rejects an image block naming a file it is still processing, so the
// wait polls files.info until the image reports its dimensions.
func TestWaitForImagesPollsUntilProcessed(t *testing.T) {
	prev := imageReadyPoll
	imageReadyPoll = time.Millisecond
	t.Cleanup(func() { imageReadyPoll = prev })

	pending := mockslack.Response{Body: map[string]any{"ok": true, "file": map[string]any{"id": "F0IMAGE001"}}}
	ready := mockslack.Response{Body: map[string]any{"ok": true, "file": map[string]any{"id": "F0IMAGE001", "original_w": 360}}}
	server := mockslack.New()
	server.Handle("files.info", pending, pending, ready)
	c := newStandardClient(t, server)

	if err := c.WaitForImages(context.Background(), []string{"F0IMAGE001"}); err != nil {
		t.Fatal(err)
	}
	if n := len(server.CallsFor("files.info")); n != 3 {
		t.Errorf("files.info calls = %d, want 3", n)
	}
}
