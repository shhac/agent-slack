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

func TestUsergroupGetByHandle(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("usergroups.list", mockslack.UsergroupsList(
		mockslack.Usergroup("S0MARKETIN", "marketing", "Marketing", "C0GENERALA"),
	))

	out, _, err := f.run(t, "usergroup", "get", "@marketing")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	if payload["id"] != "S0MARKETIN" || payload["name"] != "Marketing" {
		t.Errorf("payload = %v", payload)
	}
}

func TestUsergroupGetMultipleWithUnresolved(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("usergroups.list", mockslack.UsergroupsList(
		mockslack.Usergroup("S0MARKETIN", "marketing", "Marketing"),
	))

	out, _, err := f.run(t, "usergroup", "get", "@marketing", "@nope")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 2 {
		t.Fatalf("want 1 group + 1 meta line, got %d: %v", len(lines), lines)
	}
	if lines[0]["id"] != "S0MARKETIN" {
		t.Errorf("group = %v", lines[0])
	}
	un, ok := lines[1]["@unresolved"].([]any)
	if !ok || len(un) != 1 || un[0] != "@nope" {
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

	out, _, err := f.run(t, "usergroup", "members", "S0MARKETIN", "--resolve-users")
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

func TestUsergroupGetSingleUnresolvedErrors(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("usergroups.list", mockslack.UsergroupsList())
	out, stderr, err := f.run(t, "usergroup", "get", "@ghost")
	if err == nil {
		t.Fatalf("single unresolved arg should error; out=%q", out)
	}
	if errPayload(t, stderr)["fixable_by"] == nil {
		t.Errorf("expected a structured error on stderr: %s", stderr)
	}
}
