package cli

import (
	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// userMode is the --users flag: whether to expand referenced user ids to
// profiles, and whether to bypass the cache. It replaces the old
// --resolve-users/--refresh-users pair, where --refresh-users silently implied
// --resolve-users — the impossible "refresh but don't resolve" state is now
// unrepresentable.
type userMode string

const (
	usersNone   userMode = "none"   // ids only (default)
	usersCached userMode = "cached" // resolve, reading the user cache
	usersFresh  userMode = "fresh"  // resolve, bypassing the cache
)

func (m userMode) resolve() bool      { return m == usersCached || m == usersFresh }
func (m userMode) forceRefresh() bool { return m == usersFresh }

// registerUserMode adds the shared --users flag (default "none") with value
// completion.
func registerUserMode(cmd *cobra.Command, v *string) {
	cmd.Flags().StringVar(v, "users", string(usersNone),
		"Expand referenced user ids to profiles: none | cached | fresh (fresh bypasses the cache)")
	_ = cmd.RegisterFlagCompletionFunc("users", fixedCompletions(string(usersNone), string(usersCached), string(usersFresh)))
}

// parseUserMode validates a raw --users value (empty defaults to none).
func parseUserMode(s string) (userMode, error) {
	switch userMode(s) {
	case usersNone, usersCached, usersFresh:
		return userMode(s), nil
	case "":
		return usersNone, nil
	}
	return "", agenterrors.Newf(agenterrors.FixableByAgent, "invalid --users %q; valid: none, cached, fresh", s)
}
