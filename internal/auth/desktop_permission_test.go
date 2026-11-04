package auth

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"syscall"
	"testing"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

const (
	directDir    = "/Users/someone/Library/Application Support/Slack"
	containerDir = "/Users/someone/Library/Containers/com.tinyspeck.slackmacgap/Data/Library/Application Support/Slack"
)

func pathErr(dir string, errno syscall.Errno) error {
	return &fs.PathError{Op: "open", Path: dir + "/Local Storage/leveldb", Err: errno}
}

func TestDesktopExtractErrorHint(t *testing.T) {
	tests := []struct {
		name       string
		failures   []desktopFailure
		goos       string
		macOSMajor int
		wantHint   []string
		noHint     bool
	}{
		{
			name:       "sonoma container denial names the sandbox and macOS version",
			failures:   []desktopFailure{{baseDir: containerDir, err: pathErr(containerDir, syscall.EPERM)}},
			goos:       "darwin",
			macOSMajor: 14,
			wantHint:   []string{"Mac App Store", "macOS 14", "Full Disk Access", "sudo does not help", "agent-slack auth import-desktop", "auth import-browser"},
		},
		{
			name:       "newer macOS container denial reports the running version",
			failures:   []desktopFailure{{baseDir: containerDir, err: pathErr(containerDir, syscall.EPERM)}},
			goos:       "darwin",
			macOSMajor: 26,
			wantHint:   []string{"macOS 26", "sandbox container"},
		},
		{
			name:       "pre-sonoma container denial gets the generic privacy hint",
			failures:   []desktopFailure{{baseDir: containerDir, err: pathErr(containerDir, syscall.EPERM)}},
			goos:       "darwin",
			macOSMajor: 13,
			wantHint:   []string{"macOS privacy controls", "Full Disk Access"},
		},
		{
			name:       "unknown version gets the generic privacy hint",
			failures:   []desktopFailure{{baseDir: containerDir, err: pathErr(containerDir, syscall.EPERM)}},
			goos:       "darwin",
			macOSMajor: 0,
			wantHint:   []string{"macOS privacy controls"},
		},
		{
			name:       "non-container denial gets the generic privacy hint",
			failures:   []desktopFailure{{baseDir: directDir, err: pathErr(directDir, syscall.EPERM)}},
			goos:       "darwin",
			macOSMajor: 15,
			wantHint:   []string{"macOS privacy controls"},
		},
		{
			name: "any container denial among several failures wins",
			failures: []desktopFailure{
				{baseDir: directDir, err: errors.New("slack LevelDB did not contain localConfig_v2/v3")},
				{baseDir: containerDir, err: pathErr(containerDir, syscall.EPERM)},
			},
			goos:       "darwin",
			macOSMajor: 15,
			wantHint:   []string{"Mac App Store"},
		},
		{
			name:       "wrapped denial is still recognised",
			failures:   []desktopFailure{{baseDir: containerDir, err: fmt.Errorf("sqlite read: %w", pathErr(containerDir, syscall.EPERM))}},
			goos:       "darwin",
			macOSMajor: 15,
			wantHint:   []string{"Mac App Store"},
		},
		{
			name:       "denial text without the errno is not matched",
			failures:   []desktopFailure{{baseDir: containerDir, err: errors.New("open x: operation not permitted")}},
			goos:       "darwin",
			macOSMajor: 15,
			noHint:     true,
		},
		{
			name:       "plain file-mode denial is not a privacy hint",
			failures:   []desktopFailure{{baseDir: containerDir, err: pathErr(containerDir, syscall.EACCES)}},
			goos:       "darwin",
			macOSMajor: 15,
			noHint:     true,
		},
		{
			name:       "non-darwin never gets a macOS hint",
			failures:   []desktopFailure{{baseDir: "/home/someone/.config/Slack", err: pathErr("/home/someone/.config/Slack", syscall.EPERM)}},
			goos:       "linux",
			macOSMajor: 0,
			noHint:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := desktopExtractError(tt.failures, tt.goos, tt.macOSMajor)
			var apiErr *agenterrors.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("want *APIError, got %T", err)
			}
			if apiErr.FixableBy != agenterrors.FixableByHuman {
				t.Errorf("fixable_by = %q, want human", apiErr.FixableBy)
			}
			if !strings.Contains(apiErr.Message, tt.failures[0].baseDir) {
				t.Errorf("message %q should list the failing path", apiErr.Message)
			}
			if tt.noHint {
				if apiErr.Hint != "" {
					t.Errorf("want no hint, got %q", apiErr.Hint)
				}
				return
			}
			for _, want := range tt.wantHint {
				if !strings.Contains(apiErr.Hint, want) {
					t.Errorf("hint %q missing %q", apiErr.Hint, want)
				}
			}
		})
	}
}

func TestParseMajorVersion(t *testing.T) {
	for in, want := range map[string]int{"14.6.1": 14, "26.0\n": 26, "15": 15, "": 0, "garbage": 0} {
		if got := parseMajorVersion(in); got != want {
			t.Errorf("parseMajorVersion(%q) = %d, want %d", in, got, want)
		}
	}
}
