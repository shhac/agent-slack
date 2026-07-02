package settings

import (
	"path/filepath"
	"strings"
	"sync"
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

// Concurrent Sets model parallel MCP tool-call subprocesses writing config:
// every key must survive the read-modify-write fan-out.
func TestConcurrentSetsDoNotLoseKeys(t *testing.T) {
	isolate(t)

	var wg sync.WaitGroup
	errs := make([]error, len(CacheTTLCategories))
	for i, cat := range CacheTTLCategories {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = Set("cache.ttl."+cat, "30m")
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("set %s: %v", CacheTTLCategories[i], err)
		}
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, cat := range CacheTTLCategories {
		if got := cfg.Get("cache.ttl." + cat); got != "30m" {
			t.Errorf("cache.ttl.%s = %q after concurrent sets (lost update)", cat, got)
		}
	}
}
