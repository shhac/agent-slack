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

func TestPageByOffset(t *testing.T) {
	items := []int{0, 1, 2, 3, 4}

	// Mid-page with more remaining → a next cursor pointing at offset+limit.
	page, next := pageByOffset(items, 0, 2)
	if len(page) != 2 || page[0] != 0 || next == "" {
		t.Fatalf("page1 = %v next=%q", page, next)
	}
	if off, _ := decodeOffsetCursor(next); off != 2 {
		t.Errorf("next cursor decodes to %d, want 2", off)
	}

	// Exact-end boundary: offset+limit == len → page filled, no next.
	page, next = pageByOffset(items, 3, 2)
	if len(page) != 2 || next != "" {
		t.Errorf("end-boundary page = %v next=%q, want full page no cursor", page, next)
	}

	// Last partial page → no next.
	page, next = pageByOffset(items, 4, 2)
	if len(page) != 1 || next != "" {
		t.Errorf("last page = %v next=%q", page, next)
	}

	// Offset at/past end → empty, no next, no panic.
	if page, next = pageByOffset(items, 5, 2); page != nil || next != "" {
		t.Errorf("at-end = %v %q, want empty", page, next)
	}
	if page, next = pageByOffset(items, 99, 2); page != nil || next != "" {
		t.Errorf("past-end = %v %q, want empty", page, next)
	}
}

func TestOffsetCursorRoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, 42, 1000} {
		got, err := decodeOffsetCursor(encodeOffsetCursor(n))
		if err != nil || got != n {
			t.Errorf("round-trip %d = (%d, %v)", n, got, err)
		}
	}
	if got, err := decodeOffsetCursor(""); got != 0 || err != nil {
		t.Errorf("empty cursor = (%d, %v), want (0, nil)", got, err)
	}
	if _, err := decodeOffsetCursor("!!!not-base64"); err == nil {
		t.Error("malformed cursor should error")
	}
	// A negative offset must not decode to a usable cursor.
	if _, err := decodeOffsetCursor(encodeOffsetCursor(-1)); err == nil {
		t.Error("negative offset cursor should be rejected")
	}
}
