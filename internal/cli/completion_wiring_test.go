package cli

import (
	"strconv"
	"strings"
	"testing"
)

// completeDirective drives cobra's hidden __complete command and returns the
// trailing ":<directive>" bitmask. A command with no ValidArgsFunction yields
// ShellCompDirectiveDefault (0), which makes the shell fall back to filename
// completion — the bug this guards against. A cache-backed completer always
// sets ShellCompDirectiveNoFileComp (bit 2 == 4), even on a cold cache.
func completeDirective(t *testing.T, env *testEnv, args ...string) int {
	t.Helper()
	out, _, err := env.run(t, "", append([]string{"__complete"}, args...)...)
	if err != nil {
		t.Fatalf("__complete %v: %v", args, err)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, ":") {
			n, perr := strconv.Atoi(strings.TrimPrefix(line, ":"))
			if perr != nil {
				t.Fatalf("bad directive line %q", line)
			}
			return n
		}
	}
	t.Fatalf("no directive line in:\n%s", out)
	return 0
}

const shellCompDirectiveNoFileComp = 4

// Every channel/user/target/trigger argument and flag must offer cache-backed
// completion rather than falling back to filenames. Cold cache is fine — what
// matters is the NoFileComp directive, which proves the completer is wired.
func TestCompletionsAreWiredNotFileFallback(t *testing.T) {
	args := [][]string{
		{"workflow", "list", ""},
		{"workflow", "preview", ""},
		{"workflow", "get", ""},
		{"workflow", "run", ""},
		{"user", "get", ""},
		{"user", "dm-open", ""},
		{"later", "remind", ""},
		{"message", "get", ""},
		{"message", "list", ""},
		{"channel", "mark", ""},
		// flag-value completions
		{"workflow", "run", "--channel", ""},
		{"channel", "invite", "--channel", ""},
		{"message", "scheduled", "cancel", "--channel", ""},
	}
	for _, a := range args {
		env := newTestEnv(t)
		if d := completeDirective(t, env, a...); d&shellCompDirectiveNoFileComp == 0 {
			t.Errorf("`%s` directive = %d (no NoFileComp) — falls back to filenames", strings.Join(a, " "), d)
		}
	}
}
