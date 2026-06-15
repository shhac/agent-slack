package slack

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"testing"
)

// A DestDir whose parent is a regular file makes MkdirAll fail before any
// network call, exercising the downloadErr wrapper (which once recursed
// infinitely). The result must be a real *DownloadError.
func TestDownloadFileMkdirFailureWraps(t *testing.T) {
	c := New(Auth{Type: AuthStandard, Token: "x"})

	notADir := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := c.DownloadFile(context.Background(), DownloadOptions{
		DestDir: filepath.Join(notADir, "sub"), // parent is a file → MkdirAll errors
		URL:     "http://example.invalid/file",
	})
	if err == nil {
		t.Fatal("expected an error when DestDir cannot be created")
	}
	var de *DownloadError
	if !stderrors.As(err, &de) {
		t.Errorf("want *DownloadError, got %T: %v", err, err)
	}
}
