// Package fslock provides the primitives for safely sharing a state file
// between processes: an advisory lock serializing read-modify-write cycles,
// and an atomic write so lock-free readers never observe a torn file. The
// lock lives on a sidecar "<path>.lock" file (never the data file itself) so
// writers can atomically rename over the data file while holding the lock.
package fslock

import (
	"os"
	"path/filepath"
)

// WithLock runs fn while holding an exclusive advisory lock for path,
// blocking until the lock is free. The parent directory is created if needed
// so first-ever writes can lock before the data file exists.
func WithLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := lock(f); err != nil {
		return err
	}
	defer unlock(f)
	return fn()
}

// WriteFile replaces path's contents atomically: readers see either the old
// or the new file in full, never a truncated intermediate.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
