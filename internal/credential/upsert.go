// Upsert keying: which stored entry an incoming workspace lands on, and how
// its fields merge.
package credential

// Upsert inserts or replaces a workspace by alias and persists. An alias-less
// workspace (an import) updates the entry that uniquely holds its URL, gets a
// derived alias when the URL is new, and fails with AmbiguousURLError when
// several aliases share the URL.
func (s *Store) Upsert(ws Workspace) (Workspace, error) {
	return s.upsertMany([]Workspace{ws})
}

// UpsertMany inserts or replaces several workspaces in a single save.
func (s *Store) UpsertMany(workspaces []Workspace) error {
	if len(workspaces) == 0 {
		return nil
	}
	_, err := s.upsertMany(workspaces)
	return err
}

func (s *Store) upsertMany(workspaces []Workspace) (Workspace, error) {
	var last Workspace
	err := s.edit(func(creds *Credentials) error {
		for _, ws := range workspaces {
			normalized, err := normalizeURL(ws.URL)
			if err != nil {
				return err
			}
			ws.URL = normalized

			idx, err := upsertTarget(creds.Workspaces, &ws)
			if err != nil {
				return err
			}
			if idx == -1 {
				creds.Workspaces = append(creds.Workspaces, ws)
			} else {
				creds.Workspaces[idx] = mergeWorkspace(creds.Workspaces[idx], ws)
			}
			last = ws
			if creds.DefaultWorkspace == "" {
				creds.DefaultWorkspace = ws.Alias
			}
		}
		return nil
	})
	if err != nil {
		return Workspace{}, err
	}
	return last, nil
}

// upsertTarget decides which stored entry an upsert lands on, filling in
// ws.Alias along the way. Returns -1 when ws is a new entry. An explicit
// alias keys directly; an alias-less workspace adopts the alias of the single
// entry holding its (already normalized) URL, derives a fresh alias when the
// URL is unknown, and refuses when several aliases share the URL.
func upsertTarget(stored []Workspace, ws *Workspace) (int, error) {
	if alias := slugify(ws.Alias); alias != "" {
		ws.Alias = alias
		return findAliasIndex(stored, alias), nil
	}

	var urlMatches []int
	for i := range stored {
		if stored[i].URL == ws.URL {
			urlMatches = append(urlMatches, i)
		}
	}
	switch len(urlMatches) {
	case 1:
		ws.Alias = stored[urlMatches[0]].Alias
		return urlMatches[0], nil
	case 0:
		ws.Alias = uniquifyAlias(deriveAlias(*ws), func(a string) bool {
			return findAliasIndex(stored, a) != -1
		})
		return -1, nil
	default:
		aliases := make([]string, len(urlMatches))
		for j, idx := range urlMatches {
			aliases[j] = stored[idx].Alias
		}
		return 0, &AmbiguousURLError{URL: ws.URL, Aliases: aliases}
	}
}

// findAliasIndex returns the index of the workspace with the given alias, or
// -1 when none matches.
func findAliasIndex(workspaces []Workspace, alias string) int {
	for i := range workspaces {
		if workspaces[i].Alias == alias {
			return i
		}
	}
	return -1
}

// mergeWorkspace overlays incoming onto existing for an upsert: non-empty
// metadata fields win, and Auth is replaced wholesale (an upsert always carries
// the fresh secrets). incoming.URL is already normalized and incoming.Alias
// resolved by the caller.
func mergeWorkspace(existing, incoming Workspace) Workspace {
	existing.URL = incoming.URL
	if incoming.Name != "" {
		existing.Name = incoming.Name
	}
	if incoming.TeamID != "" {
		existing.TeamID = incoming.TeamID
	}
	if incoming.UserID != "" {
		existing.UserID = incoming.UserID
	}
	if incoming.TeamDomain != "" {
		existing.TeamDomain = incoming.TeamDomain
	}
	existing.Auth = incoming.Auth
	return existing
}
