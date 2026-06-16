package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

var sendNow = time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

func TestBuildSendRequestMatrix(t *testing.T) {
	cases := []struct {
		name    string
		kind    render.TargetKind
		text    string
		flags   sendFlags
		wantErr string
	}{
		{"plain text ok", render.TargetChannel, "hi", sendFlags{}, ""},
		{"no content", render.TargetChannel, "", sendFlags{}, "text is required"},
		{"attach without text ok", render.TargetChannel, "", sendFlags{attach: []string{"/tmp/x"}}, ""},
		{"schedule + attach", render.TargetChannel, "hi", sendFlags{attach: []string{"/tmp/x"}, scheduleFlags: scheduleFlags{scheduleIn: "3h"}}, "cannot be combined with --attach"},
		{"blocks + attach", render.TargetChannel, "hi", sendFlags{attach: []string{"/tmp/x"}, blocksPath: "/tmp/b.json"}, "--blocks cannot be combined"},
		{"broadcast to DM", render.TargetUser, "hi", sendFlags{replyBroadcast: true}, "not supported for DM targets"},
		{"broadcast without thread", render.TargetChannel, "hi", sendFlags{replyBroadcast: true}, "requires --thread-ts"},
		{"broadcast with thread ok", render.TargetChannel, "hi", sendFlags{replyBroadcast: true, threadTS: "1.000001"}, ""},
		{"broadcast on permalink ok", render.TargetURL, "hi", sendFlags{replyBroadcast: true}, ""},
		{"bad schedule", render.TargetChannel, "hi", sendFlags{scheduleFlags: scheduleFlags{schedule: "whenever"}}, "invalid --schedule"},
	}
	for _, tc := range cases {
		req, err := buildSendRequest(strings.NewReader(""), tc.kind, tc.text, tc.flags, sendNow)
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", tc.name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: expected error containing %q, got request %+v", tc.name, tc.wantErr, req)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: error %q does not contain %q", tc.name, err, tc.wantErr)
		}
	}
}

func TestBuildSendRequestFormatting(t *testing.T) {
	req, err := buildSendRequest(strings.NewReader(""), render.TargetChannel, "ping @U05BRPTKL6A:\n- one\n- two", sendFlags{}, sendNow)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.text, "<@U05BRPTKL6A>") {
		t.Errorf("text = %q, want promoted mention", req.text)
	}
	if len(req.blocks) == 0 {
		t.Error("list text should produce rich_text blocks")
	}
	// Plain text produces no blocks.
	plain, err := buildSendRequest(strings.NewReader(""), render.TargetChannel, "just words", sendFlags{}, sendNow)
	if err != nil || plain.blocks != nil {
		t.Errorf("plain blocks = %v, err %v", plain.blocks, err)
	}
}

func TestSendPayloadBuilders(t *testing.T) {
	posted := postedMessagePayload(slack.PostResult{ChannelID: "D9", TS: "2.000002"}, "https://acme.slack.com", "1.000001")
	if posted["channel_id"] != "D9" || posted["ts"] != "2.000002" || posted["thread_ts"] != "1.000001" {
		t.Errorf("posted = %v", posted)
	}
	if posted["permalink"] != "https://acme.slack.com/archives/D9/p2000002?thread_ts=1.000001&cid=D9" {
		t.Errorf("permalink = %v", posted["permalink"])
	}

	// No ts echoed → no permalink, no ts key.
	bare := postedMessagePayload(slack.PostResult{ChannelID: "C1"}, "https://acme.slack.com", "")
	if _, has := bare["ts"]; has {
		t.Errorf("bare = %v", bare)
	}

	scheduled := scheduleResultPayload(slack.ScheduleResult{ChannelID: "C1", ScheduledMessageID: "Q1", PostAt: 1781000000}, "1.000001")
	if scheduled["scheduled_message_id"] != "Q1" || scheduled["post_at"] != int64(1781000000) {
		t.Errorf("scheduled = %v", scheduled)
	}
}

func TestResolveListMode(t *testing.T) {
	cases := []struct {
		name                 string
		kind                 render.TargetKind
		ts, threadTS, oldest string
		filters              bool
		want                 listMode
		wantErr              bool
	}{
		{"url thread", render.TargetURL, "", "", "", false, listModeURLThread, false},
		{"url with filters", render.TargetURL, "", "", "", true, 0, true},
		{"history", render.TargetChannel, "", "", "", false, listModeHistory, false},
		{"history filters need oldest", render.TargetChannel, "", "", "", true, 0, true},
		{"history filters with oldest", render.TargetChannel, "", "", "1.0", true, listModeHistory, false},
		{"thread by ts", render.TargetChannel, "1.0", "", "", false, listModeThread, false},
		{"thread by thread-ts", render.TargetChannel, "", "1.0", "", false, listModeThread, false},
		{"thread with filters", render.TargetChannel, "1.0", "", "", true, 0, true},
		{"user target", render.TargetUser, "", "", "", false, 0, true},
	}
	for _, tc := range cases {
		got, err := resolveListMode(tc.kind, tc.ts, tc.threadTS, tc.oldest, tc.filters)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
			continue
		}
		if err == nil && got != tc.want {
			t.Errorf("%s: mode = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestLoadBlocksFromPath(t *testing.T) {
	// stdin
	blocks, err := loadBlocksFromPath(strings.NewReader(`[{"type":"section"}]`), "-")
	if err != nil || len(blocks) != 1 {
		t.Errorf("stdin: blocks = %v, err = %v", blocks, err)
	}
	// file
	path := filepath.Join(t.TempDir(), "blocks.json")
	if err := os.WriteFile(path, []byte(`[{"type":"divider"},{"type":"section"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	blocks, err = loadBlocksFromPath(nil, path)
	if err != nil || len(blocks) != 2 {
		t.Errorf("file: blocks = %v, err = %v", blocks, err)
	}
	// error cases
	for name, input := range map[string]string{
		"not an array":      `{"type":"section"}`,
		"non-object member": `[{"type":"section"}, 42]`,
		"invalid json":      `{`,
	} {
		if _, err := loadBlocksFromPath(strings.NewReader(input), "-"); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := loadBlocksFromPath(nil, "/no/such/blocks.json"); err == nil {
		t.Error("missing file: expected error")
	}
}
