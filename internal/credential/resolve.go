package credential

import (
	"net/url"
	"sort"
	"strings"
)

// ResolveDefault returns the default workspace, or the first one.
func (s *Store) ResolveDefault() (*Workspace, error) {
	creds, err := s.Load()
	if err != nil {
		return nil, err
	}
	if creds.DefaultWorkspaceURL != "" {
		for i := range creds.Workspaces {
			if creds.Workspaces[i].URL == creds.DefaultWorkspaceURL {
				return &creds.Workspaces[i], nil
			}
		}
	}
	if len(creds.Workspaces) > 0 {
		return &creds.Workspaces[0], nil
	}
	return nil, ErrWorkspaceNotFound
}

// Resolve picks a workspace for a --workspace selector. An empty selector
// returns the default. A selector is matched as: exact normalized-URL match,
// else a unique case-insensitive substring of the URL, host, host without the
// .slack.com suffix, name, or team domain.
func (s *Store) Resolve(selector string) (*Workspace, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return s.ResolveDefault()
	}
	creds, err := s.Load()
	if err != nil {
		return nil, err
	}

	if normalized, nerr := normalizeURL(selector); nerr == nil {
		want := strings.ToLower(normalized)
		for i := range creds.Workspaces {
			if strings.ToLower(creds.Workspaces[i].URL) == want {
				return &creds.Workspaces[i], nil
			}
		}
	}

	needle := strings.ToLower(selector)
	var matches []int
	for i := range creds.Workspaces {
		for _, cand := range selectorCandidates(creds.Workspaces[i]) {
			if cand != "" && strings.Contains(cand, needle) {
				matches = append(matches, i)
				break
			}
		}
	}
	switch len(matches) {
	case 0:
		return nil, ErrWorkspaceNotFound
	case 1:
		return &creds.Workspaces[matches[0]], nil
	default:
		labels := make([]string, len(matches))
		for j, idx := range matches {
			labels[j] = creds.Workspaces[idx].URL
		}
		sort.Strings(labels)
		return nil, &AmbiguousSelectorError{Selector: selector, Matches: labels}
	}
}

func selectorCandidates(w Workspace) []string {
	host := ""
	if u, err := url.Parse(w.URL); err == nil {
		host = strings.ToLower(u.Host)
	}
	return []string{
		strings.ToLower(w.URL),
		host,
		strings.TrimSuffix(host, ".slack.com"),
		strings.ToLower(w.Name),
		strings.ToLower(w.TeamDomain),
	}
}
