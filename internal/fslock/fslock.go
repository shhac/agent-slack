// Package fslock provides the primitives for safely sharing a state file
// between processes: an advisory lock serializing read-modify-write cycles,
// and an atomic write so lock-free readers never observe a torn file. The
// lock lives on a sidecar "<path>.lock" file (never the data file itself) so
// writers can atomically rename over the data file while holding the lock.
package fslock

import (
	"encoding/json"
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

// ReadJSON reads path into a fresh T. A missing or corrupt file yields the
// zero T and ok=false — the permissive contract agent-slack's state files
// share (an absent or torn file reads as empty, never partially decoded).
// Read errors other than absence are returned.
func ReadJSON[T any](path string) (v *T, ok bool, err error) {
	var zero T
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &zero, false, nil
		}
		return nil, false, err
	}
	v = new(T)
	if json.Unmarshal(data, v) != nil {
		return &zero, false, nil
	}
	return v, true, nil
}

// WriteJSON marshals v (indented) and writes it atomically with the shared
// state-file permissions: 0o700 directory, 0o600 file.
func WriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return WriteFile(path, data, 0o600)
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
