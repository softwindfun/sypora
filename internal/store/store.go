package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type FileState struct {
	LocalPath   string
	RemoteKey   string
	LocalMTime  time.Time
	LocalSize   int64
	RemoteETag  string
	RemoteMTime time.Time
	RemoteSize  int64
	SyncTime    time.Time
}

type Store struct {
	db *sql.DB
}

func dbPath() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	dir := filepath.Join(appData, "Sypora")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "sypora.db"), nil
}

func Open() (*Store, error) {
	path, err := dbPath()
	if err != nil {
		return nil, fmt.Errorf("store path: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	db.SetMaxOpenConns(1) // SQLite single writer

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS file_state (
			local_path   TEXT PRIMARY KEY,
			remote_key   TEXT NOT NULL,
			local_mtime  TEXT NOT NULL DEFAULT '',
			local_size   INTEGER NOT NULL DEFAULT 0,
			remote_etag  TEXT NOT NULL DEFAULT '',
			remote_mtime TEXT NOT NULL DEFAULT '',
			remote_size  INTEGER NOT NULL DEFAULT 0,
			sync_time    TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_remote_key ON file_state(remote_key);
	`)
	return err
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Upsert(fs FileState) error {
	_, err := s.db.Exec(`
		INSERT INTO file_state (local_path, remote_key, local_mtime, local_size, remote_etag, remote_mtime, remote_size, sync_time)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(local_path) DO UPDATE SET
			remote_key   = excluded.remote_key,
			local_mtime  = excluded.local_mtime,
			local_size   = excluded.local_size,
			remote_etag  = excluded.remote_etag,
			remote_mtime = excluded.remote_mtime,
			remote_size  = excluded.remote_size,
			sync_time    = excluded.sync_time
	`, fs.LocalPath, fs.RemoteKey,
		fs.LocalMTime.Format(time.RFC3339), fs.LocalSize,
		fs.RemoteETag, fs.RemoteMTime.Format(time.RFC3339), fs.RemoteSize,
		fs.SyncTime.Format(time.RFC3339))
	return err
}

func (s *Store) Get(localPath string) (*FileState, error) {
	row := s.db.QueryRow(
		"SELECT local_path, remote_key, local_mtime, local_size, remote_etag, remote_mtime, remote_size, sync_time FROM file_state WHERE local_path = ?",
		localPath)
	return scanFileState(row)
}

func (s *Store) GetByRemoteKey(remoteKey string) (*FileState, error) {
	row := s.db.QueryRow(
		"SELECT local_path, remote_key, local_mtime, local_size, remote_etag, remote_mtime, remote_size, sync_time FROM file_state WHERE remote_key = ?",
		remoteKey)
	return scanFileState(row)
}

func (s *Store) Delete(localPath string) error {
	_, err := s.db.Exec("DELETE FROM file_state WHERE local_path = ?", localPath)
	return err
}

func (s *Store) DeleteByRemoteKey(remoteKey string) error {
	_, err := s.db.Exec("DELETE FROM file_state WHERE remote_key = ?", remoteKey)
	return err
}

func (s *Store) AllByPrefix(localPrefix string) ([]FileState, error) {
	rows, err := s.db.Query(
		"SELECT local_path, remote_key, local_mtime, local_size, remote_etag, remote_mtime, remote_size, sync_time FROM file_state WHERE local_path LIKE ?",
		localPrefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var states []FileState
	for rows.Next() {
		fs, err := scanFileStateFromRows(rows)
		if err != nil {
			return nil, err
		}
		states = append(states, *fs)
	}
	return states, rows.Err()
}

func (s *Store) AllRemoteByPrefix(remotePrefix string) ([]FileState, error) {
	rows, err := s.db.Query(
		"SELECT local_path, remote_key, local_mtime, local_size, remote_etag, remote_mtime, remote_size, sync_time FROM file_state WHERE remote_key LIKE ?",
		remotePrefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var states []FileState
	for rows.Next() {
		fs, err := scanFileStateFromRows(rows)
		if err != nil {
			return nil, err
		}
		states = append(states, *fs)
	}
	return states, rows.Err()
}

func scanFileState(row *sql.Row) (*FileState, error) {
	var fs FileState
	var lm, rm, st string
	err := row.Scan(&fs.LocalPath, &fs.RemoteKey, &lm, &fs.LocalSize, &fs.RemoteETag, &rm, &fs.RemoteSize, &st)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	fs.LocalMTime, _ = time.Parse(time.RFC3339, lm)
	fs.RemoteMTime, _ = time.Parse(time.RFC3339, rm)
	fs.SyncTime, _ = time.Parse(time.RFC3339, st)
	return &fs, nil
}

func scanFileStateFromRows(rows *sql.Rows) (*FileState, error) {
	var fs FileState
	var lm, rm, st string
	err := rows.Scan(&fs.LocalPath, &fs.RemoteKey, &lm, &fs.LocalSize, &fs.RemoteETag, &rm, &fs.RemoteSize, &st)
	if err != nil {
		return nil, err
	}
	fs.LocalMTime, _ = time.Parse(time.RFC3339, lm)
	fs.RemoteMTime, _ = time.Parse(time.RFC3339, rm)
	fs.SyncTime, _ = time.Parse(time.RFC3339, st)
	return &fs, nil
}
