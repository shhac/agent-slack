package slack

import (
	"context"
	"reflect"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func TestToCompactUser(t *testing.T) {
	got := ToCompactUser(map[string]any{
		"id":   "U1",
		"name": "paul",
		"tz":   "Europe/London",
		"profile": map[string]any{
			"real_name":    "Paul Somers",
			"display_name": "paul",
			"email":        "paul@example.com",
			"title":        "Engineer",
		},
		"is_bot":  false,
		"deleted": false,
	})
	want := CompactUser{
		ID: "U1", Name: "paul", RealName: "Paul Somers", DisplayName: "paul",
		Email: "paul@example.com", Title: "Engineer", TZ: "Europe/London",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}

	// Top-level real_name wins over profile.real_name.
	topLevel := ToCompactUser(map[string]any{"id": "U2", "real_name": "Top", "profile": map[string]any{"real_name": "Nested"}})
	if topLevel.RealName != "Top" {
		t.Errorf("RealName = %q", topLevel.RealName)
	}
}

func TestResolveUserIDPassthrough(t *testing.T) {
	c := New(Auth{Type: AuthStandard, Token: "x"}) // must not hit the API
	got, err := ResolveUserID(context.Background(), c, " U12345ABCDE ")
	if err != nil || got != "U12345ABCDE" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestResolveUserIDByEmail(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.lookupByEmail", map[string]any{
		"ok":   true,
		"user": map[string]any{"id": "U0EMAIL"},
	})
	c := newStandardClient(t, server)

	got, err := ResolveUserID(context.Background(), c, "bob@example.com")
	if err != nil || got != "U0EMAIL" {
		t.Fatalf("got %q, %v", got, err)
	}
	if email := server.CallsFor("users.lookupByEmail")[0].Params.Get("email"); email != "bob@example.com" {
		t.Errorf("email param = %q", email)
	}
}

func TestResolveUserIDEmailFallsBackToScan(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.lookupByEmail", map[string]any{"ok": false, "error": "users_not_found"})
	server.HandleBody("users.list", map[string]any{
		"ok": true,
		"members": []any{
			map[string]any{"id": "U1", "name": "alice", "profile": map[string]any{"email": "Bob@Example.com"}},
		},
	})
	c := newStandardClient(t, server)

	got, err := ResolveUserID(context.Background(), c, "bob@example.com")
	if err != nil || got != "U1" {
		t.Fatalf("got %q, %v (case-insensitive email scan)", got, err)
	}
}

func TestResolveUserIDByHandle(t *testing.T) {
	server := mockslack.New()
	server.Handle("users.list",
		mockslack.Response{Body: map[string]any{
			"ok":                true,
			"members":           []any{map[string]any{"id": "U1", "name": "alice"}},
			"response_metadata": map[string]any{"next_cursor": "p2"},
		}},
		mockslack.Response{Body: map[string]any{
			"ok":      true,
			"members": []any{map[string]any{"id": "U2", "name": "bob"}},
		}},
	)
	c := newStandardClient(t, server)

	got, err := ResolveUserID(context.Background(), c, "@Bob")
	if err != nil || got != "U2" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestResolveUserIDNotFound(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.list", map[string]any{"ok": true, "members": []any{}})
	c := newStandardClient(t, server)

	if _, err := ResolveUserID(context.Background(), c, "@ghost"); err == nil {
		t.Fatal("expected error")
	}
}
