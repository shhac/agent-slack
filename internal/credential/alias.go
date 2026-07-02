package credential

import (
	"fmt"
	"net/url"
	"strings"
)

// deriveAlias picks a human alias for a workspace that arrived without one
// (imports, v1 migration): team domain, else URL host minus .slack.com, else
// the workspace name. Callers uniquify against the store.
func deriveAlias(ws Workspace) string {
	if a := slugify(ws.TeamDomain); a != "" {
		return a
	}
	if u, err := url.Parse(ws.URL); err == nil {
		if a := slugify(strings.TrimSuffix(strings.ToLower(u.Host), ".slack.com")); a != "" {
			return a
		}
	}
	if a := slugify(ws.Name); a != "" {
		return a
	}
	return "workspace"
}

// slugify lowercases and collapses every non-alphanumeric run to a single
// hyphen, so an alias is always safe as a Keychain account segment and a
// shell-friendly selector.
func slugify(s string) string {
	var b strings.Builder
	pendingHyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		switch {
		case isAlnum:
			if pendingHyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingHyphen = false
			b.WriteRune(r)
		default:
			pendingHyphen = true
		}
	}
	return b.String()
}

// uniquifyAlias returns base, or base-2/base-3/… if taken.
func uniquifyAlias(base string, taken func(string) bool) string {
	if !taken(base) {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !taken(candidate) {
			return candidate
		}
	}
}
