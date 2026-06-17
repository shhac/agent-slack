package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/slack"
)

func decodeNoticeLine(t *testing.T, line string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("notice line not JSON: %q (%v)", line, err)
	}
	return m
}

func TestRateLimitNoticeRendering(t *testing.T) {
	var buf bytes.Buffer
	globals := &GlobalFlags{stderr: &buf}
	notify := rateLimitNotice(globals)

	notify(slack.RateLimitNotice{Method: "conversations.history", RetryAfter: 2 * time.Second, Delay: 2 * time.Second, Attempt: 1, WillRetry: true})
	notify(slack.RateLimitNotice{Method: "conversations.history", RetryAfter: 90 * time.Second, Delay: 60 * time.Second, Attempt: 2, WillRetry: true})
	notify(slack.RateLimitNotice{Method: "conversations.history", Attempt: 4, WillRetry: false})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d notice lines, want 3:\n%s", len(lines), buf.String())
	}

	// Retry without cap divergence: a wait message, no hint, no "capped" detail.
	retry := decodeNoticeLine(t, lines[0])
	if _, ok := retry["hint"]; ok {
		t.Errorf("retry notice should have no hint: %v", retry)
	}
	if msg, _ := retry["notice"].(string); !strings.Contains(msg, "waiting 2s before retry (attempt 1)") || strings.Contains(msg, "capped") {
		t.Errorf("retry notice = %q", msg)
	}

	// Retry where Slack asked for longer than the cap: surface the uncapped ask.
	capped := decodeNoticeLine(t, lines[1])
	if msg, _ := capped["notice"].(string); !strings.Contains(msg, "Slack asked for 1m30s, capped to 1m0s") {
		t.Errorf("capped notice = %q", msg)
	}

	// Terminal hit: carries the load-bearing non-Marketplace tier hint.
	terminal := decodeNoticeLine(t, lines[2])
	if msg, _ := terminal["notice"].(string); !strings.Contains(msg, "gave up after 4 attempts") {
		t.Errorf("terminal notice = %q", msg)
	}
	if hint, _ := terminal["hint"].(string); !strings.Contains(hint, "non-Marketplace tier") || !strings.Contains(hint, "internal/custom app token") {
		t.Errorf("terminal hint = %q", hint)
	}
}

func TestRateLimitNoticeNilStderrNoPanic(t *testing.T) {
	notify := rateLimitNotice(&GlobalFlags{}) // stderr nil
	notify(slack.RateLimitNotice{Method: "m", Delay: time.Second, Attempt: 1, WillRetry: true})
	notify(slack.RateLimitNotice{Method: "m", Attempt: 2, WillRetry: false})
}
