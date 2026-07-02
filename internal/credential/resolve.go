package credential

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

func resolveDefault(creds *Credentials) (*Workspace, error) {
	if creds.DefaultWorkspace != "" {
		if idx := findAliasIndex(creds.Workspaces, creds.DefaultWorkspace); idx != -1 {
			return &creds.Workspaces[idx], nil
		}
	}
	if len(creds.Workspaces) > 0 {
		return &creds.Workspaces[0], nil
	}
	return nil, ErrWorkspaceNotFound
}

// Resolve picks a workspace for a --workspace selector. An empty selector
// returns the default. Matching order: exact alias; exact normalized URL
// (only while unique — several aliases may share a URL); then a unique
// case-insensitive substring of the alias, URL, host, host without the
// .slack.com suffix, name, or team domain.
func (s *Store) Resolve(selector string) (*Workspace, error) {
	creds, err := s.Load()
	if err != nil {
		return nil, err
	}
	return resolveWorkspace(creds, selector)
}

// resolveWorkspace is Resolve over already-loaded credentials, shared with
// mutations that resolve inside a locked section (where Load must not
// re-enter the lock via migration).
func resolveWorkspace(creds *Credentials, selector string) (*Workspace, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return resolveDefault(creds)
	}

	needle := strings.ToLower(selector)
	if idx := findAliasIndex(creds.Workspaces, needle); idx != -1 {
		return &creds.Workspaces[idx], nil
	}

	if normalized, nerr := normalizeURL(selector); nerr == nil {
		want := strings.ToLower(normalized)
		var matches []int
		for i := range creds.Workspaces {
			if strings.ToLower(creds.Workspaces[i].URL) == want {
				matches = append(matches, i)
			}
		}
		if len(matches) > 0 {
			return pickMatch(creds, selector, matches)
		}
	}

	var matches []int
	for i := range creds.Workspaces {
		for _, cand := range selectorCandidates(creds.Workspaces[i]) {
			if cand != "" && strings.Contains(cand, needle) {
				matches = append(matches, i)
				break
			}
		}
	}
	if len(matches) == 0 {
		return nil, ErrWorkspaceNotFound
	}
	return pickMatch(creds, selector, matches)
}

// pickMatch returns the single match, or an AmbiguousSelectorError naming
// each candidate as "alias (url)".
func pickMatch(creds *Credentials, selector string, matches []int) (*Workspace, error) {
	if len(matches) == 1 {
		return &creds.Workspaces[matches[0]], nil
	}
	labels := make([]string, len(matches))
	for j, idx := range matches {
		w := creds.Workspaces[idx]
		labels[j] = fmt.Sprintf("%s (%s)", w.Alias, w.URL)
	}
	sort.Strings(labels)
	return nil, &AmbiguousSelectorError{Selector: selector, Matches: labels}
}

func selectorCandidates(w Workspace) []string {
	host := ""
	if u, err := url.Parse(w.URL); err == nil {
		host = strings.ToLower(u.Host)
	}
	return []string{
		w.Alias,
		strings.ToLower(w.URL),
		host,
		strings.TrimSuffix(host, ".slack.com"),
		strings.ToLower(w.Name),
		strings.ToLower(w.TeamDomain),
	}
}
