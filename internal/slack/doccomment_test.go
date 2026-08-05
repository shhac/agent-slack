package slack

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// A blank line between a comment block and the declaration below it silently
// detaches the doc comment — godoc stops seeing it, and gofmt, vet, and
// golangci-lint all stay quiet. Moving code between files is exactly when this
// happens: a file split here detached fifteen comments and orphaned five more,
// and nothing in the verification loop noticed.
func TestEventFilesHaveNoDetachedDocComments(t *testing.T) {
	out, err := exec.Command("git", "ls-files", "event*.go").Output()
	if err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	declRe := regexp.MustCompile(`^(func |type |const |var )`)

	for _, file := range strings.Fields(string(out)) {
		body, err := exec.Command("cat", file).Output()
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		lines := strings.Split(string(body), "\n")
		for i, line := range lines {
			if strings.TrimSpace(line) != "" || i == 0 || i+1 >= len(lines) {
				continue
			}
			if !strings.HasPrefix(strings.TrimSpace(lines[i-1]), "//") {
				continue
			}
			if declRe.MatchString(lines[i+1]) {
				t.Errorf("%s:%d: blank line detaches the comment above from %q",
					file, i+1, strings.TrimSpace(lines[i+1]))
			}
		}
	}
}
