package cli

import (
	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// resolveMode is the --resolve flag: whether to expand referenced entities
// (users, channels, usergroups) in content to richer data, and whether to
// bypass the cache. Mentions arrive as bare ids in rich_text, so resolving is
// the only way to make them legible. It replaced the old
// --resolve-users/--refresh-users pair, where --refresh-users silently implied
// --resolve-users — the impossible "refresh but don't resolve" state is now
// unrepresentable.
type resolveMode string

const (
	resolveNone   resolveMode = "none"   // ids only (default)
	resolveCached resolveMode = "cached" // resolve, reading the cache
	resolveFresh  resolveMode = "fresh"  // resolve, bypassing the cache
)

func (m resolveMode) resolve() bool      { return m == resolveCached || m == resolveFresh }
func (m resolveMode) forceRefresh() bool { return m == resolveFresh }

// registerResolveFlag adds the shared --resolve flag (default "none") with value
// completion.
func registerResolveFlag(cmd *cobra.Command, v *string) {
	cmd.Flags().StringVar(v, "resolve", string(resolveNone),
		"Expand referenced users/channels/usergroups to profiles: none | cached | fresh (fresh bypasses the cache)")
	_ = cmd.RegisterFlagCompletionFunc("resolve", fixedCompletions(string(resolveNone), string(resolveCached), string(resolveFresh)))
}

// parseResolveMode validates a raw --resolve value (empty defaults to none).
func parseResolveMode(s string) (resolveMode, error) {
	switch resolveMode(s) {
	case resolveNone, resolveCached, resolveFresh:
		return resolveMode(s), nil
	case "":
		return resolveNone, nil
	}
	return "", agenterrors.Newf(agenterrors.FixableByAgent, "invalid --resolve %q; valid: none, cached, fresh", s)
}
