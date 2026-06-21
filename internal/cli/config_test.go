package cli

import (
	"path/filepath"
	"testing"
)

func TestConfigGetNDJSON(t *testing.T) {
	t.Setenv("AGENT_SLACK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	env := newTestEnv(t)

	// Single key → one NDJSON line with {key, value}.
	out, _, err := env.run(t, "", "config", "get", "cache.ttl.users")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d: %s", len(lines), out)
	}
	if lines[0]["key"] != "cache.ttl.users" {
		t.Errorf("key = %v", lines[0]["key"])
	}
	// Unset key returns empty string value (not absent).
	if v, ok := lines[0]["value"]; !ok || v != "" {
		t.Errorf("unset key value = %v (%v)", v, ok)
	}
}

func TestConfigGetMultiNDJSON(t *testing.T) {
	t.Setenv("AGENT_SLACK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	env := newTestEnv(t)

	// Multiple keys → one NDJSON line per key, in input order.
	out, _, err := env.run(t, "", "config", "get", "cache.ttl.users", "cache.ttl.channels")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %s", len(lines), out)
	}
	if lines[0]["key"] != "cache.ttl.users" || lines[1]["key"] != "cache.ttl.channels" {
		t.Errorf("keys out of order: %v", lines)
	}
}

func TestConfigGetUnknownKeyUnresolved(t *testing.T) {
	t.Setenv("AGENT_SLACK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	env := newTestEnv(t)

	// Unknown key → @unresolved control line on stdout (exit 0).
	out, _, err := env.run(t, "", "config", "get", "no.such.key")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d: %s", len(lines), out)
	}
	u, ok := lines[0]["@unresolved"].(map[string]any)
	if !ok {
		t.Fatalf("expected @unresolved control line, got %v", lines[0])
	}
	if u["id"] != "no.such.key" || u["fixable_by"] != "agent" {
		t.Errorf("@unresolved = %v", u)
	}
}

func TestConfigGetFormatJSON(t *testing.T) {
	t.Setenv("AGENT_SLACK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	env := newTestEnv(t)

	// --format json wraps results in a {data:[…]} envelope.
	out, _, err := env.run(t, "", "config", "get", "--format", "json", "cache.ttl.users")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	data, ok := payload["data"].([]any)
	if !ok || len(data) != 1 {
		t.Fatalf("--format json want {data:[…]}, got %v", payload)
	}
	row := data[0].(map[string]any)
	if row["key"] != "cache.ttl.users" {
		t.Errorf("row = %v", row)
	}
}
