// The named store mutations, each a single edit(...) cycle.
package credential

// SetIdentity records the Slack team_id/user_id (resolved from auth.test) on
// the aliased workspace and persists. These are non-secret and key the
// on-disk cache namespace. It deliberately never touches Auth, so a
// best-effort identity backfill can't clobber stored secrets. An unknown
// alias is a no-op.
func (s *Store) SetIdentity(alias, teamID, userID string) error {
	return s.edit(func(creds *Credentials) error {
		idx := findAliasIndex(creds.Workspaces, alias)
		if idx == -1 {
			return errNoChange
		}
		w := &creds.Workspaces[idx]
		changed := false
		if teamID != "" && w.TeamID != teamID {
			w.TeamID = teamID
			changed = true
		}
		if userID != "" && w.UserID != userID {
			w.UserID = userID
			changed = true
		}
		if !changed {
			return errNoChange
		}
		return nil
	})
}

// SetDefault resolves selector to a stored workspace and makes its alias the
// default.
func (s *Store) SetDefault(selector string) error {
	return s.edit(func(creds *Credentials) error {
		ws, err := resolveWorkspace(creds, selector)
		if err != nil {
			return err
		}
		creds.DefaultWorkspace = ws.Alias
		return nil
	})
}

// Remove resolves selector to one stored workspace and deletes it along with
// its Keychain secrets. Other aliases for the same URL are untouched.
func (s *Store) Remove(selector string) error {
	return s.edit(func(creds *Credentials) error {
		ws, err := resolveWorkspace(creds, selector)
		if err != nil {
			return err
		}
		alias := ws.Alias
		kept := creds.Workspaces[:0]
		for _, w := range creds.Workspaces {
			if w.Alias == alias {
				s.kc.Delete(xoxcAccount(alias))
				s.kc.Delete(tokenAccount(alias))
				s.kc.Delete(xoxdAccount(alias))
				continue
			}
			kept = append(kept, w)
		}
		creds.Workspaces = kept
		if creds.DefaultWorkspace == alias {
			creds.DefaultWorkspace = ""
			if len(creds.Workspaces) > 0 {
				creds.DefaultWorkspace = creds.Workspaces[0].Alias
			}
		}
		return nil
	})
}
