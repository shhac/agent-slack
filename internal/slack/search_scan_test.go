package slack

import (
	"testing"

	"github.com/shhac/agent-slack/internal/render"
)

func TestChannelScanFilterMatch(t *testing.T) {
	filter := channelScanFilter{
		queryLower: "deploy",
		userID:     "U11111111",
		afterSec:   1000,
		beforeSec:  2000,
	}
	msg := func(ts, user, text string) render.MessageSummary {
		return render.MessageSummary{TS: ts, User: user, Text: text}
	}
	cases := []struct {
		name             string
		m                render.MessageSummary
		keep, pastOldest bool
	}{
		{"match", msg("1500.000001", "U11111111", "the DEPLOY worked"), true, false},
		{"too new", msg("2500.000001", "U11111111", "deploy"), false, false},
		{"too old stops the scan", msg("500.000001", "U11111111", "deploy"), false, true},
		{"wrong author", msg("1500.000001", "U22222222", "deploy"), false, false},
		{"content miss", msg("1500.000001", "U11111111", "release notes"), false, false},
		{"unparseable ts skips the window", msg("not-a-ts", "U11111111", "deploy"), true, false},
	}
	for _, tc := range cases {
		keep, pastOldest := filter.match(tc.m)
		if keep != tc.keep || pastOldest != tc.pastOldest {
			t.Errorf("%s: (keep,pastOldest) = (%v,%v), want (%v,%v)", tc.name, keep, pastOldest, tc.keep, tc.pastOldest)
		}
	}

	// Unbounded filter keeps everything.
	open := channelScanFilter{afterSec: -1, beforeSec: -1}
	if keep, stop := open.match(msg("1.000001", "U1", "anything")); !keep || stop {
		t.Errorf("open filter = (%v,%v)", keep, stop)
	}
	// Query matches rendered content (blocks), not just raw text.
	blockFilter := channelScanFilter{queryLower: "from blocks", afterSec: -1, beforeSec: -1}
	withBlocks := render.MessageSummary{TS: "1.000001", Blocks: []any{map[string]any{
		"type": "section", "text": map[string]any{"type": "mrkdwn", "text": "hello from blocks"},
	}}}
	if keep, _ := blockFilter.match(withBlocks); !keep {
		t.Error("filter should match content rendered from blocks")
	}
}

func TestTotalPages(t *testing.T) {
	cases := []struct {
		container map[string]any
		want      int
	}{
		{map[string]any{"paging": map[string]any{"pages": float64(3)}}, 3},
		{map[string]any{"pagination": map[string]any{"pages": float64(2)}}, 2},
		{map[string]any{"pagination": map[string]any{"page_count": float64(4)}}, 4},
		{map[string]any{}, 0},
		{nil, 0},
	}
	for _, tc := range cases {
		if got := totalPages(tc.container); got != tc.want {
			t.Errorf("totalPages(%v) = %d, want %d", tc.container, got, tc.want)
		}
	}
}
