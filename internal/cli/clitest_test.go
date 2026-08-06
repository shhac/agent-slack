package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/credential"
	"github.com/shhac/agent-slack/internal/mockslack"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

// Fixtures carry a resolved identity so the common command path keys its cache
// offline (no bootstrap auth.test). Bootstrap is exercised separately.
// Slack-shaped on purpose: an id with an underscore is not recognised as an
// id at all, so a fixture using one silently exercises a name-lookup path the
// real flow never takes. internal/mockslack pins the same rule for its own
// fixtures.
const (
	fixtureTeamID = "T0FAKETEAM"
	fixtureUserID = "U0FAKEPAUL1"
)

func fixtureCacheKey() string { return slack.IdentityCacheKey(fixtureTeamID, fixtureUserID) }

// cliFixture is a hermetic test env + mockslack server. Commands run with
// --base-url pointed at the mock so the standard-token transport lands there.
type cliFixture struct {
	env    *testEnv
	server *mockslack.Server
	url    string
}

func newCLIFixture(t *testing.T) *cliFixture {
	t.Helper()
	env := newTestEnv(t)
	server := mockslack.New()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	if _, err := env.store.Upsert(credential.Workspace{
		URL:    "https://acme.slack.com",
		Name:   "Acme",
		TeamID: fixtureTeamID,
		UserID: fixtureUserID,
		Auth:   credential.Auth{Type: credential.AuthStandard, Token: "xoxb-test-token"},
	}); err != nil {
		t.Fatal(err)
	}
	// Keep the user cache + downloads out of the real home dir.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	return &cliFixture{env: env, server: server, url: ts.URL}
}

// newBrowserCLIFixture is newCLIFixture with browser (xoxc) auth, whose
// workspace URL is the mock server so the browser transport hits it. Use for
// client-only paths (drafts, scheduling on browser auth).
func newBrowserCLIFixture(t *testing.T) *cliFixture {
	t.Helper()
	env := newTestEnv(t)
	server := mockslack.New()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	if _, err := env.store.Upsert(credential.Workspace{
		URL:    ts.URL,
		Name:   "Acme",
		TeamID: fixtureTeamID,
		UserID: fixtureUserID,
		Auth:   credential.Auth{Type: credential.AuthBrowser, XOXC: "xoxc-test", XOXD: "xoxd-test"},
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	return &cliFixture{env: env, server: server, url: ts.URL}
}

func (f *cliFixture) run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	full := append([]string{"--base-url", f.url}, args...)
	return f.env.run(t, "", full...)
}

func parseJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, s)
	}
	return m
}

func parseNDJSON(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line not JSON: %v\n%s", err, line)
		}
		out = append(out, m)
	}
	return out
}

func errPayload(t *testing.T, stderr string) map[string]any {
	t.Helper()
	return parseJSON(t, stderr)
}

// historyWith builds a conversations.history body with the given messages.
func historyWith(messages ...map[string]any) map[string]any {
	return mockslack.History(messages...)
}

func simpleMessage(ts, user, text string) map[string]any {
	return mockslack.Message(ts, user, text)
}

// resolvableChannel makes "#general" resolve to channelID. It fixtures
// workspace STATE rather than the resolver's strategy: both the in:#name
// search trick and the conversations.list fallback are answered, so tests
// survive a change to how ResolveChannelID works — and real search.messages
// fixtures in the same test don't collide with the resolution call.
func (f *cliFixture) resolvableChannel(channelID string) {
	f.server.HandleWhen("search.messages", func(p url.Values) bool {
		return strings.HasPrefix(p.Get("query"), "in:#")
	}, mockslack.Response{Body: mockslack.SearchMessages(mockslack.ChannelMatch(channelID))})
	f.server.HandleBody("conversations.list", mockslack.ConversationsList(mockslack.Channel(channelID, "general")))
}

// fileHost serves canvas/file bytes for download tests.
func fileHost(t *testing.T, contentType, body string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// The same rule internal/mockslack pins for its fixtures: an id that is not
// Slack-shaped routes tests through name resolution instead of id handling,
// so the path under test is not the path production takes.
func TestFixtureIDsAreSlackShaped(t *testing.T) {
	if !render.IsUserID(fixtureUserID) {
		t.Errorf("fixtureUserID %q is not a Slack-shaped user id", fixtureUserID)
	}
	if !strings.Contains(fixtureUserID, "FAKE") || !strings.Contains(fixtureTeamID, "FAKE") {
		t.Error("fixture ids should be self-evidently fabricated")
	}
}
