package slack

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadURL(t *testing.T) {
	cases := []struct {
		name              string
		isCanvas          bool
		private, download string
		want              string
	}{
		{"normal prefers download", false, "P", "D", "D"},
		{"normal falls back to private", false, "P", "", "P"},
		{"canvas prefers private", true, "P", "D", "P"},
		{"canvas falls back to download", true, "", "D", "D"},
		{"neither set", false, "", "", ""},
		{"canvas neither set", true, "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := downloadURL(tc.isCanvas, tc.private, tc.download); got != tc.want {
				t.Errorf("downloadURL(%v, %q, %q) = %q, want %q", tc.isCanvas, tc.private, tc.download, got, tc.want)
			}
		})
	}
}

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
