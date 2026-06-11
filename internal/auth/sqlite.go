package auth

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// copySqliteForRead copies a SQLite database (plus any -wal/-shm sidecars) to a
// fresh temp dir so it can be read while the owning app holds a lock. The
// returned cleanup removes the temp dir.
func copySqliteForRead(dbPath string) (copyPath string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp("", "agent-slack-sqlite-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup = func() { _ = os.RemoveAll(tmpDir) }

	copyPath = filepath.Join(tmpDir, filepath.Base(dbPath))
	if err := copyFile(dbPath, copyPath); err != nil {
		cleanup()
		return "", func() {}, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = copyFile(dbPath+suffix, copyPath+suffix) // best effort
	}
	return copyPath, cleanup, nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

// queryReadonlySqlite runs a read-only query against a SQLite file via the
// pure-Go modernc driver and returns rows as maps keyed by column name.
func queryReadonlySqlite(dbPath, query string) ([]map[string]any, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var out []map[string]any
	for rows.Next() {
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = cells[i]
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite read: %w", err)
	}
	return out, nil
}

func rowString(row map[string]any, key string) string {
	switch v := row[key].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return ""
	}
}

func rowBytes(row map[string]any, key string) []byte {
	switch v := row[key].(type) {
	case []byte:
		return v
	case string:
		return []byte(v)
	default:
		return nil
	}
}
