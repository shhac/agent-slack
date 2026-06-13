package settings

import (
	"path/filepath"
	"strings"
	"testing"
)

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("AGENT_SLACK_CONFIG", filepath.Join(t.TempDir(), "config.json"))
}

func TestSetGetUnset(t *testing.T) {
	isolate(t)

	if err := Set("cache.ttl.channels", "30m"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil || cfg.Get("cache.ttl.channels") != "30m" {
		t.Fatalf("get = %q, err %v", cfg.Get("cache.ttl.channels"), err)
	}
	if cfg.Version != 1 {
		t.Errorf("version = %d", cfg.Version)
	}

	if err := Unset("cache.ttl.channels"); err != nil {
		t.Fatal(err)
	}
	cfg, _ = Load()
	if cfg.Get("cache.ttl.channels") != "" {
		t.Error("unset did not remove the key")
	}
}

func TestSetValidation(t *testing.T) {
	isolate(t)

	if err := Set("cache.ttl.nope", "5m"); err == nil || !strings.Contains(err.Error(), "unknown config key") {
		t.Errorf("bad key err = %v", err)
	}
	if err := Set("cache.ttl.get", "soon"); err == nil || !strings.Contains(err.Error(), "invalid duration") {
		t.Errorf("bad duration err = %v", err)
	}
	// "0" is a valid TTL (disables reads for that category).
	if err := Set("cache.ttl.get", "0"); err != nil {
		t.Errorf("0 should be valid: %v", err)
	}
}

func TestCacheTTLOverrides(t *testing.T) {
	isolate(t)
	_ = Set("cache.ttl.channels", "30m")
	_ = Set("cache.ttl.get", "2m")
	cfg, _ := Load()
	ov := cfg.CacheTTLOverrides()
	if ov["channels"] != "30m" || ov["get"] != "2m" {
		t.Errorf("overrides = %v", ov)
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	isolate(t)
	cfg, err := Load()
	if err != nil || len(cfg.Settings) != 0 {
		t.Errorf("fresh config should be empty: %+v, %v", cfg, err)
	}
}
