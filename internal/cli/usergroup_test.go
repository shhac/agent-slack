package cli

import (
	"net/url"
	"testing"

	"github.com/shhac/agent-slack/internal/mockslack"
)

func TestUsergroupList(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("usergroups.list", mockslack.UsergroupsList(
		mockslack.Usergroup("S0MARKETIN", "marketing", "Marketing", "C0GENERALA", "C0ANNOUNCE"),
		mockslack.Usergroup("S0ENGINEER", "eng", "Engineering"),
	))

	out, _, err := f.run(t, "usergroup", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 2 {
		t.Fatalf("want 2 usergroups, got %d: %v", len(lines), lines)
	}
	if lines[0]["id"] != "S0MARKETIN" || lines[0]["handle"] != "marketing" {
		t.Errorf("row 0 = %v", lines[0])
	}
	// Default channels surface as a list — the caller picks where to post; the
	// CLI takes no "best channel" view.
	chans, _ := lines[0]["channels"].([]any)
	if len(chans) != 2 || chans[0] != "C0GENERALA" {
		t.Errorf("channels = %v", lines[0]["channels"])
	}
	// A group with no default channels omits the field rather than emitting [].
	if _, present := lines[1]["channels"]; present {
		t.Errorf("empty channels should be omitted, got %v", lines[1])
	}
}

func TestUsergroupListPaginationMeta(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("usergroups.list", mockslack.UsergroupsList(
		mockslack.Usergroup("S0MARKETIN", "marketing", "Marketing"),
		mockslack.Usergroup("S0ENGINEER", "eng", "Engineering"),
		mockslack.Usergroup("S0DESIGNNN", "design", "Design"),
	))

	out, _, err := f.run(t, "usergroup", "list", "--limit", "2")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	// 2 rows + a trailing @pagination meta line.
	if len(lines) != 3 {
		t.Fatalf("want 2 rows + pagination meta, got %d: %v", len(lines), lines)
	}
	pg, ok := lines[2]["@pagination"].(map[string]any)
	if !ok || pg["next_cursor"] == nil {
		t.Fatalf("pagination meta = %v", lines[2])
	}

	// Page 2 reads the cached full set (no second usergroups.list) and returns
	// the remaining group with no further cursor.
	out2, _, err := f.run(t, "usergroup", "list", "--limit", "2", "--cursor", pg["next_cursor"].(string))
	if err != nil {
		t.Fatal(err)
	}
	lines2 := parseNDJSON(t, out2)
	if len(lines2) != 1 {
		t.Fatalf("page 2 = %d lines, want 1 row no cursor: %v", len(lines2), lines2)
	}
	if n := len(f.server.CallsFor("usergroups.list")); n != 1 {
		t.Errorf("usergroups.list called %d times; paging should reuse the cached full set (want 1)", n)
	}
}

func TestUsergroupGetByHandle(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("usergroups.list", mockslack.UsergroupsList(
		mockslack.Usergroup("S0MARKETIN", "marketing", "Marketing", "C0GENERALA"),
	))

	out, _, err := f.run(t, "usergroup", "get", "@marketing")
	if err != nil {
		t.Fatal(err)
	}
	// Single arg → one NDJSON line (EntityGet default).
	lines := parseNDJSON(t, out)
	if len(lines) != 1 || lines[0]["id"] != "S0MARKETIN" || lines[0]["name"] != "Marketing" {
		t.Errorf("out = %s", out)
	}
}

func TestUsergroupGetMultipleWithUnresolved(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("usergroups.list", mockslack.UsergroupsList(
		mockslack.Usergroup("S0MARKETIN", "marketing", "Marketing"),
	))

	// Multi-get → NDJSON in input order; the miss becomes an interleaved
	// {"@unresolved":{id,reason,fixable_by:"agent"}} record, exit 0.
	out, _, err := f.run(t, "usergroup", "get", "@marketing", "@nope")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 2 {
		t.Fatalf("want 1 group + 1 @unresolved, got %d: %v", len(lines), lines)
	}
	if lines[0]["id"] != "S0MARKETIN" {
		t.Errorf("group = %v", lines[0])
	}
	un, ok := lines[1]["@unresolved"].(map[string]any)
	if !ok || un["id"] != "@nope" || un["fixable_by"] != "agent" {
		t.Errorf("@unresolved = %v", lines[1])
	}
}

func TestUsergroupMembers(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("usergroups.list", mockslack.UsergroupsList(
		mockslack.Usergroup("S0MARKETIN", "marketing", "Marketing"),
	))
	f.server.HandleWhen("usergroups.users.list",
		func(p url.Values) bool { return p.Get("usergroup") == "S0MARKETIN" },
		mockslack.Response{Body: mockslack.UsergroupUsers("U0ALICEAA", "U0BOBBBBB")})

	out, _, err := f.run(t, "usergroup", "members", "@marketing")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 2 || lines[0]["id"] != "U0ALICEAA" || lines[1]["id"] != "U0BOBBBBB" {
		t.Errorf("members = %v", lines)
	}
}

// members by id skips usergroups.list (the id resolves directly), and
// --resolve-users expands ids to compact profiles.
func TestUsergroupMembersByIDResolveUsers(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleWhen("usergroups.users.list",
		func(p url.Values) bool { return p.Get("usergroup") == "S0MARKETIN" },
		mockslack.Response{Body: mockslack.UsergroupUsers("U0ALICEAA")})
	f.server.HandleBody("users.info", mockslack.UserInfo("U0ALICEAA", "alice"))

	out, _, err := f.run(t, "usergroup", "members", "S0MARKETIN", "--resolve", "auto")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 1 || lines[0]["name"] != "alice" {
		t.Errorf("resolved members = %v", lines)
	}
	if calls := f.server.CallsFor("usergroups.list"); len(calls) != 0 {
		t.Errorf("an S… id should not trigger usergroups.list, got %d calls", len(calls))
	}
}

func TestUsergroupGetSingleUnresolved(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("usergroups.list", mockslack.UsergroupsList())
	out, _, err := f.run(t, "usergroup", "get", "@ghost")
	if err != nil {
		t.Fatalf("sole miss should exit 0; err=%v", err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 1 {
		t.Fatalf("want one @unresolved line, got %d: %v", len(lines), lines)
	}
	un, ok := lines[0]["@unresolved"].(map[string]any)
	if !ok || un["id"] != "@ghost" || un["fixable_by"] != "agent" {
		t.Errorf("@unresolved = %v", lines[0])
	}
}
