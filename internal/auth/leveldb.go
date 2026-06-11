package auth

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

// readSlackLocalConfig reads the most recent localConfig_v2/v3 value out of a
// Chromium Local Storage LevelDB directory. The directory is snapshotted to a
// temp location first because Slack Desktop keeps the DB open.
func readSlackLocalConfig(leveldbDir string) ([]byte, error) {
	if _, err := os.Stat(leveldbDir); err != nil {
		return nil, err
	}
	snap, cleanup, err := snapshotDir(leveldbDir)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	// A leftover LOCK in the copy can block opening; remove it.
	_ = os.Remove(filepath.Join(snap, "LOCK"))

	db, err := leveldb.OpenFile(snap, &opt.Options{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	needle := []byte("localConfig_v")
	v2 := []byte("localConfig_v2")
	v3 := []byte("localConfig_v3")

	var best []byte
	var bestRank uint64
	found := false

	iter := db.NewIterator(nil, nil)
	defer iter.Release()
	for iter.Next() {
		key := iter.Key()
		if !bytes.Contains(key, needle) {
			continue
		}
		if !bytes.Contains(key, v2) && !bytes.Contains(key, v3) {
			continue
		}
		val := iter.Value()
		if len(val) == 0 {
			continue
		}
		var rank uint64
		if len(key) >= 8 {
			rank = binary.LittleEndian.Uint64(key[len(key)-8:])
		}
		if !found || rank >= bestRank {
			best = append(best[:0], val...)
			bestRank = rank
			found = true
		}
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("slack LevelDB did not contain localConfig_v2/v3")
	}
	return best, nil
}

// snapshotDir copies a directory tree to a fresh temp dir and returns it with a
// cleanup func.
func snapshotDir(srcDir string) (string, func(), error) {
	dst, err := os.MkdirTemp("", "agent-slack-leveldb-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dst) }

	err = filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files (e.g. transient lock files)
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	return dst, cleanup, nil
}
