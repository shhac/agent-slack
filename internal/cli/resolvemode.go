package cli

import (
	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/slack"
)

// resolveMode is the --resolve flag: whether to expand referenced entities
// (users, channels, usergroups) in content to richer data, and how the cache is
// used. Mentions arrive as bare ids in rich_text, so resolving is the only way
// to make them legible.
//
//   - none   — don't resolve; ids stay bare
//   - cached — resolve from cache only; never fetch
//   - auto   — (default) cache, then fetch the misses, UNLESS the category was
//     fully warmed within its completeness window (then a miss is trusted as
//     authoritative and the fetch is skipped); hints toward `cache warm`
//   - fresh  — ignore cached reads and refetch
type resolveMode string

const (
	resolveNone   resolveMode = "none"
	resolveCached resolveMode = "cached"
	resolveAuto   resolveMode = "auto"
	resolveFresh  resolveMode = "fresh"
)

// resolve reports whether any resolution happens at all.
func (m resolveMode) resolve() bool { return m != resolveNone && m != "" }

// policy maps the mode to the slack-layer cache policy.
func (m resolveMode) policy() slack.ResolvePolicy {
	switch m {
	case resolveNone:
		return slack.ResolveOff
	case resolveCached:
		return slack.ResolveCacheOnly
	case resolveFresh:
		return slack.ResolveBypassCache
	default: // auto (and unset)
		return slack.ResolveCacheThenFetch
	}
}

// registerResolveFlag adds the shared --resolve flag with value completion. def
// is the per-command default — auto for message reads (a few mentions, cheap and
// legible), none for members lists (bulk: opt in to expand hundreds of profiles).
func registerResolveFlag(cmd *cobra.Command, v *string, def resolveMode) {
	cmd.Flags().StringVar(v, "resolve", string(def),
		"Expand referenced users/channels/usergroups: none | cached (cache only) | auto (cache, fetch misses) | fresh (refetch)")
	_ = cmd.RegisterFlagCompletionFunc("resolve", fixedCompletions(
		string(resolveNone), string(resolveCached), string(resolveAuto), string(resolveFresh)))
}

// parseResolveMode validates a raw --resolve value (empty defaults to auto).
func parseResolveMode(s string) (resolveMode, error) {
	switch resolveMode(s) {
	case resolveNone, resolveCached, resolveAuto, resolveFresh:
		return resolveMode(s), nil
	case "":
		return resolveAuto, nil
	}
	return "", agenterrors.Newf(agenterrors.FixableByAgent, "invalid --resolve %q; valid: none, cached, auto, fresh", s)
}
