package slack

import (
	"context"
	"strings"
	"testing"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/mockslack"
)

func TestNormalizeChannelInput(t *testing.T) {
	cases := []struct {
		input, id, name string
	}{
		{"#general", "", "general"},
		{"general", "", "general"},
		{"C060RS20UMV", "C060RS20UMV", ""},
		{" #ops ", "", "ops"},
		{"", "", ""},
	}
	for _, tc := range cases {
		id, name := NormalizeChannelInput(tc.input)
		if id != tc.id || name != tc.name {
			t.Errorf("NormalizeChannelInput(%q) = (%q, %q), want (%q, %q)", tc.input, id, name, tc.id, tc.name)
		}
	}
}

func TestResolveChannelIDPassthrough(t *testing.T) {
	c := New(Auth{Type: AuthStandard, Token: "x"}) // no server: must not call the API
	got, err := ResolveChannelID(context.Background(), c, "C060RS20UMV")
	if err != nil || got != "C060RS20UMV" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestResolveChannelIDViaSearch(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("search.messages", map[string]any{
		"ok": true,
		"messages": map[string]any{
			"matches": []any{map[string]any{"channel": map[string]any{"id": "C0SEARCHED"}}},
		},
	})
	c := newStandardClient(t, server)

	got, err := ResolveChannelID(context.Background(), c, "#general")
	if err != nil || got != "C0SEARCHED" {
		t.Fatalf("got %q, %v", got, err)
	}
	if q := server.CallsFor("search.messages")[0].Params.Get("query"); q != "in:#general" {
		t.Errorf("query = %q", q)
	}
	if len(server.CallsFor("conversations.list")) != 0 {
		t.Error("should not fall back to pagination when search resolves")
	}
}

func TestResolveChannelIDFallbackPagination(t *testing.T) {
	server := mockslack.New()
	// Token without search:read scope — the resolver must fall through.
	server.HandleBody("search.messages", map[string]any{"ok": false, "error": "not_allowed_token_type"})
	server.Handle("conversations.list",
		mockslack.Response{Body: map[string]any{
			"ok":                true,
			"channels":          []any{map[string]any{"id": "C1", "name": "random"}},
			"response_metadata": map[string]any{"next_cursor": "page2"},
		}},
		mockslack.Response{Body: map[string]any{
			"ok":       true,
			"channels": []any{map[string]any{"id": "C2", "name": "general"}},
		}},
	)
	c := newStandardClient(t, server)

	got, err := ResolveChannelID(context.Background(), c, "general")
	if err != nil || got != "C2" {
		t.Fatalf("got %q, %v", got, err)
	}
	if pages := len(server.CallsFor("conversations.list")); pages != 2 {
		t.Errorf("paginated %d pages, want 2", pages)
	}
}

func TestResolveChannelIDNotFound(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("search.messages", map[string]any{"ok": true, "messages": map[string]any{"matches": []any{}}})
	server.HandleBody("conversations.list", map[string]any{"ok": true, "channels": []any{}})
	c := newStandardClient(t, server)

	_, err := ResolveChannelID(context.Background(), c, "#nope")
	var apiErr *agenterrors.APIError
	if !agenterrors.As(err, &apiErr) || apiErr.FixableBy != agenterrors.FixableByAgent {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(apiErr.Hint, "channel list") {
		t.Errorf("hint = %q, want pointer to channel list", apiErr.Hint)
	}
}

func TestResolveChannelName(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("conversations.info", map[string]any{
		"ok":      true,
		"channel": map[string]any{"id": "C1", "name": "general"},
	})
	c := newStandardClient(t, server)
	if got := ResolveChannelName(context.Background(), c, "C1"); got != "general" {
		t.Errorf("got %q", got)
	}
}

func TestResolveChannelNameDM(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("conversations.info", map[string]any{
		"ok":      true,
		"channel": map[string]any{"id": "D1", "is_im": true, "user": "U1"},
	})
	server.HandleBody("users.info", map[string]any{
		"ok":   true,
		"user": map[string]any{"id": "U1", "profile": map[string]any{"display_name": "paul"}},
	})
	c := newStandardClient(t, server)
	if got := ResolveChannelName(context.Background(), c, "D1"); got != "paul" {
		t.Errorf("got %q", got)
	}
}

func TestResolveChannelNameFallsBackToID(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("conversations.info", map[string]any{"ok": false, "error": "channel_not_found"})
	c := newStandardClient(t, server)
	if got := ResolveChannelName(context.Background(), c, "C404"); got != "C404" {
		t.Errorf("got %q, want raw ID on failure", got)
	}
}
