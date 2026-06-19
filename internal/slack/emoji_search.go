package slack

import (
	"cmp"
	"context"
	"math"
	"slices"
	"strings"
)

// EmojiMatch is one ranked hit from `emoji search`: a custom emoji plus the
// tier it matched in and a 0–1 score (higher is better).
type EmojiMatch struct {
	Name     string  `json:"name"`
	URL      string  `json:"url,omitempty"`       // image URL (only with Full)
	AliasFor string  `json:"alias_for,omitempty"` // target name when this is an alias
	Match    string  `json:"match"`               // tier: exact|prefix|token_prefix|contains|fuzzy
	Score    float64 `json:"score"`
}

// SearchEmojiOptions controls SearchEmoji.
type SearchEmojiOptions struct {
	Limit  int    // page size; <=0 uses defaultEmojiSearchLimit, capped at maxEmojiSearchLimit
	Cursor string // opaque offset cursor from a previous page
	Full   bool   // include image URLs in the results
}

const (
	defaultEmojiSearchLimit = 20
	maxEmojiSearchLimit     = 100
)

// SearchEmoji fuzzy-ranks the workspace's custom emoji against query and
// returns one page plus the cursor for the next (empty when exhausted). The
// custom set is matched, not the standard unicode set — those are well-known
// and resolved by name via `emoji get`. Ranking is tiered (exact, prefix,
// token prefix, substring, edit-distance fuzzy); see scoreEmoji.
func SearchEmoji(ctx context.Context, c *Client, query string, opts SearchEmojiOptions) ([]EmojiMatch, string, error) {
	offset, err := decodeOffsetCursor(opts.Cursor)
	if err != nil {
		return nil, "", err
	}
	limit := clampInt(orDefault(opts.Limit, defaultEmojiSearchLimit), 1, maxEmojiSearchLimit)

	byName, err := c.customEmojiMap(ctx)
	if err != nil {
		return nil, "", err
	}
	ranked := rankEmoji(query, byName, opts.Full)
	page, next := pageByOffset(ranked, offset, limit)
	return page, next, nil
}

// rankEmoji scores every custom emoji against query, drops non-matches, and
// orders by score desc, then shorter name, then name — a total order so paging
// is stable. URLs are stripped unless full.
func rankEmoji(query string, custom map[string]CustomEmoji, full bool) []EmojiMatch {
	q := foldEmojiKey(query)
	if q == "" {
		return nil
	}
	matches := make([]EmojiMatch, 0, len(custom))
	for name, e := range custom {
		score, tier := scoreEmoji(q, name)
		if tier == "" {
			continue
		}
		m := EmojiMatch{Name: name, AliasFor: e.AliasFor, Match: tier, Score: score}
		if full {
			m.URL = e.URL
		}
		matches = append(matches, m)
	}
	slices.SortFunc(matches, func(a, b EmojiMatch) int {
		if a.Score != b.Score {
			return cmp.Compare(b.Score, a.Score) // descending
		}
		if len(a.Name) != len(b.Name) {
			return len(a.Name) - len(b.Name)
		}
		return strings.Compare(a.Name, b.Name)
	})
	return matches
}

// scoreEmoji ranks one candidate name against an already-folded query, in
// tiers. Within a tier the score is nudged by how much of the name the query
// covers, so a tighter match sorts first. Returns ("" tier) for no match.
//
// Normalization differs from `emoji get`: for SEARCH we fold case AND collapse
// separators (-_+), so ":party_parrot:", "party-parrot", and "partyparrot" all
// match the same query. (`get` stays exact — it must not conflate distinct
// names.)
func scoreEmoji(foldedQuery, name string) (float64, string) {
	nameKey := foldEmojiKey(name)
	if nameKey == "" {
		return 0, ""
	}
	coverage := float64(len(foldedQuery)) / float64(len(nameKey))
	if coverage > 1 {
		coverage = 1
	}
	switch {
	case nameKey == foldedQuery:
		return 1.0, "exact"
	case strings.HasPrefix(nameKey, foldedQuery):
		return round(0.8 + 0.1*coverage), "prefix"
	case anyTokenHasPrefix(name, foldedQuery):
		return round(0.6 + 0.1*coverage), "token_prefix"
	case strings.Contains(nameKey, foldedQuery):
		return round(0.4 + 0.1*coverage), "contains"
	}
	maxDist := 1
	if len(foldedQuery) > 4 {
		maxDist = 2
	}
	if d, ok := boundedLevenshtein(foldedQuery, nameKey, maxDist); ok {
		return round(0.2 + 0.1*(1-float64(d)/float64(maxDist+1))), "fuzzy"
	}
	return 0, ""
}

// anyTokenHasPrefix reports whether a separator-delimited token of name begins
// with the folded query — so "parrot" finds "party-parrot".
func anyTokenHasPrefix(name, foldedQuery string) bool {
	for _, tok := range strings.FieldsFunc(strings.ToLower(name), isEmojiSep) {
		if strings.HasPrefix(tok, foldedQuery) {
			return true
		}
	}
	return false
}

func isEmojiSep(r rune) bool { return r == '-' || r == '_' || r == '+' }

// foldEmojiKey collapses a name/query for fuzzy matching: trim, strip colons,
// lowercase, and remove separators (-_+). Search-only — see scoreEmoji.
func foldEmojiKey(s string) string {
	return strings.Map(func(r rune) rune {
		if isEmojiSep(r) {
			return -1
		}
		return r
	}, trimEmojiColons(s))
}

// boundedLevenshtein returns the edit distance between a and b when it is <=
// maxDist, else (0, false). Bounded so a fuzzy sweep over thousands of short
// names stays cheap.
func boundedLevenshtein(a, b string, maxDist int) (int, bool) {
	la, lb := len(a), len(b)
	if abs(la-lb) > maxDist {
		return 0, false
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		rowMin := curr[0]
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
			if curr[j] < rowMin {
				rowMin = curr[j]
			}
		}
		if rowMin > maxDist {
			return 0, false // every path already exceeds the bound
		}
		prev, curr = curr, prev
	}
	if prev[lb] > maxDist {
		return 0, false
	}
	return prev[lb], true
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// round to 3 decimals so scores compare cleanly and don't carry float noise.
func round(f float64) float64 {
	return math.Round(f*1000) / 1000
}
