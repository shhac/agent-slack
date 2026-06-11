package cli

import (
	"testing"
)

func TestAPICall(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("team.info", map[string]any{"ok": true, "team": map[string]any{"id": "T1", "name": "Acme"}})

	out, _, err := f.run(t, "api", "call", "team.info", "--params", `{"team":"T1"}`)
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	if payload["team"].(map[string]any)["name"] != "Acme" {
		t.Errorf("payload = %v", payload)
	}
	if got := f.server.CallsFor("team.info")[0].Params.Get("team"); got != "T1" {
		t.Errorf("param = %q", got)
	}
}
