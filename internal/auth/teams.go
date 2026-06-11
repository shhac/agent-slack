package auth

import (
	"encoding/json"
	"sort"
	"strings"
)

// teamsFromMap converts a decoded Slack `teams` object (keyed by team id) into
// a slice of Teams, keeping only entries with a usable url and an xoxc- token.
// The result is sorted by url for deterministic output.
func teamsFromMap(raw map[string]json.RawMessage) []Team {
	var teams []Team
	for _, entry := range raw {
		var t struct {
			URL   string `json:"url"`
			Name  string `json:"name"`
			Token string `json:"token"`
		}
		if err := json.Unmarshal(entry, &t); err != nil {
			continue
		}
		if t.URL == "" || !strings.HasPrefix(t.Token, "xoxc-") {
			continue
		}
		teams = append(teams, Team{URL: t.URL, Name: t.Name, Token: t.Token})
	}
	sort.Slice(teams, func(i, j int) bool { return teams[i].URL < teams[j].URL })
	return teams
}

// parseTeamsJSON parses a JSON object of Slack teams (the value of
// `localStorage.localConfig_v2.teams`, as returned by the browser importers)
// into Teams.
func parseTeamsJSON(raw []byte) []Team {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return teamsFromMap(m)
}
