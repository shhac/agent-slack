package cli

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shhac/agent-slack/internal/credential"
	"github.com/shhac/agent-slack/internal/mockslack"
)

// cliFixture is a hermetic store + mockslack server. Commands run with
// --base-url pointed at the mock so the standard-token transport lands there.
type cliFixture struct {
	store  *credential.Store
	server *mockslack.Server
	url    string
}

func newCLIFixture(t *testing.T) *cliFixture {
	t.Helper()
	store := useHermeticStore(t)
	server := mockslack.New()
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)

	if _, err := store.Upsert(credential.Workspace{
		URL:  "https://acme.slack.com",
		Name: "Acme",
		Auth: credential.Auth{Type: credential.AuthStandard, Token: "xoxb-test-token"},
	}); err != nil {
		t.Fatal(err)
	}
	// Keep the user cache + downloads out of the real home dir.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	return &cliFixture{store: store, server: server, url: ts.URL}
}

func (f *cliFixture) run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	full := append([]string{"--base-url", f.url}, args...)
	return runCLI(t, "", full...)
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
	items := make([]any, len(messages))
	for i, m := range messages {
		items[i] = m
	}
	return map[string]any{"ok": true, "messages": items}
}

func simpleMessage(ts, user, text string) map[string]any {
	return map[string]any{"ts": ts, "user": user, "text": text, "type": "message"}
}

// resolvableChannel makes "#general" resolve to channelID via the
// search.messages one-call trick.
func (f *cliFixture) resolvableChannel(channelID string) {
	f.server.HandleBody("search.messages", map[string]any{
		"ok": true,
		"messages": map[string]any{
			"matches": []any{map[string]any{"channel": map[string]any{"id": channelID}}},
		},
	})
}
