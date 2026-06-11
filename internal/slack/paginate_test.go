package slack

import (
	"context"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func TestEachPage(t *testing.T) {
	server := mockslack.New()
	server.Handle("users.list",
		mockslack.Response{Body: map[string]any{
			"ok":                true,
			"members":           []any{map[string]any{"id": "U1"}},
			"response_metadata": map[string]any{"next_cursor": "cur2"},
		}},
		mockslack.Response{Body: map[string]any{
			"ok":      true,
			"members": []any{map[string]any{"id": "U2"}},
		}},
	)
	c := newStandardClient(t, server)

	var pages int
	err := EachPage(context.Background(), c, "users.list", map[string]any{"limit": 200}, func(resp map[string]any) (bool, error) {
		pages++
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if pages != 2 {
		t.Errorf("pages = %d", pages)
	}
	calls := server.CallsFor("users.list")
	if calls[0].Params.Has("cursor") {
		t.Error("first page should not send a cursor")
	}
	if got := calls[1].Params.Get("cursor"); got != "cur2" {
		t.Errorf("second page cursor = %q", got)
	}
}

func TestEachPageEarlyStop(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("users.list", map[string]any{
		"ok":                true,
		"response_metadata": map[string]any{"next_cursor": "more"},
	})
	c := newStandardClient(t, server)

	pages := 0
	err := EachPage(context.Background(), c, "users.list", nil, func(resp map[string]any) (bool, error) {
		pages++
		return false, nil
	})
	if err != nil || pages != 1 {
		t.Errorf("pages = %d, err = %v", pages, err)
	}
}
