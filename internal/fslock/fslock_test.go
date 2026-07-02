package fslock

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWithLockMutualExclusion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")

	// Each WithLock opens its own file description, so same-process goroutines
	// exercise the same advisory-lock path separate processes would.
	var active, overlaps atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := WithLock(path, func() error {
				if active.Add(1) != 1 {
					overlaps.Add(1)
				}
				time.Sleep(2 * time.Millisecond)
				active.Add(-1)
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := overlaps.Load(); n != 0 {
		t.Errorf("critical section overlapped %d times", n)
	}
}

func TestWithLockCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "data.json")
	if err := WithLock(path, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestWithLockReleasesOnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	sentinel := errors.New("boom")
	if err := WithLock(path, func() error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}

	done := make(chan error, 1)
	go func() { done <- WithLock(path, func() error { return nil }) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("lock not released after fn error")
	}
}

func TestWriteFileReplacesAtomicallyAndKeepsPerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	if err := WriteFile(path, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "v2" {
		t.Fatalf("content = %q, err %v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 600", perm)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "data.json" {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}
