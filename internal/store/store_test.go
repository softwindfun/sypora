package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenClose(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	s, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	path := filepath.Join(dir, "Sypora", "sypora.db")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("db file was not created")
	}
}

func TestUpsertAndGet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	s, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	now := time.Now()
	fs := FileState{
		LocalPath:   `C:\notes\test.md`,
		RemoteKey:   "notes/test.md",
		LocalMTime:  now,
		LocalSize:   100,
		RemoteETag:  "abc123",
		RemoteMTime: now,
		RemoteSize:  100,
		SyncTime:    now,
	}

	if err := s.Upsert(fs); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := s.Get(`C:\notes\test.md`)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.RemoteETag != "abc123" {
		t.Fatalf("ETag mismatch: %s", got.RemoteETag)
	}
	if got.LocalSize != 100 {
		t.Fatalf("Size mismatch: %d", got.LocalSize)
	}
}

func TestUpsertUpdate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	s, _ := Open()
	defer s.Close()

	now := time.Now()
	s.Upsert(FileState{
		LocalPath:  `C:\notes\test.md`,
		RemoteKey:  "notes/test.md",
		RemoteETag: "old",
		SyncTime:   now,
	})

	// Update with new ETag
	s.Upsert(FileState{
		LocalPath:  `C:\notes\test.md`,
		RemoteKey:  "notes/test.md",
		RemoteETag: "new",
		SyncTime:   now,
	})

	got, _ := s.Get(`C:\notes\test.md`)
	if got.RemoteETag != "new" {
		t.Fatalf("expected new ETag, got: %s", got.RemoteETag)
	}
}

func TestGetByRemoteKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	s, _ := Open()
	defer s.Close()

	now := time.Now()
	s.Upsert(FileState{
		LocalPath: `C:\notes\a.md`,
		RemoteKey: "notes/a.md",
		SyncTime:  now,
	})

	got, err := s.GetByRemoteKey("notes/a.md")
	if err != nil {
		t.Fatalf("GetByRemoteKey: %v", err)
	}
	if got == nil {
		t.Fatal("should find by remote key")
	}

	got, err = s.GetByRemoteKey("nonexistent")
	if err != nil {
		t.Fatalf("GetByRemoteKey: %v", err)
	}
	if got != nil {
		t.Fatal("should not find nonexistent key")
	}
}

func TestDelete(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	s, _ := Open()
	defer s.Close()

	now := time.Now()
	s.Upsert(FileState{
		LocalPath: `C:\notes\todel.md`,
		RemoteKey: "notes/todel.md",
		SyncTime:  now,
	})

	s.Delete(`C:\notes\todel.md`)

	got, _ := s.Get(`C:\notes\todel.md`)
	if got != nil {
		t.Fatal("should be deleted")
	}
}

func TestAllByPrefix(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	s, _ := Open()
	defer s.Close()

	now := time.Now()
	s.Upsert(FileState{LocalPath: `C:\notes\a.md`, RemoteKey: "notes/a.md", SyncTime: now})
	s.Upsert(FileState{LocalPath: `C:\notes\sub\b.md`, RemoteKey: "notes/sub/b.md", SyncTime: now})
	s.Upsert(FileState{LocalPath: `C:\other\c.md`, RemoteKey: "other/c.md", SyncTime: now})

	// Should find 2 files under C:\notes
	files, err := s.AllByPrefix(`C:\notes`)
	if err != nil {
		t.Fatalf("AllByPrefix: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2, got %d", len(files))
	}
}

func TestAllRemoteByPrefix(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	s, _ := Open()
	defer s.Close()

	now := time.Now()
	s.Upsert(FileState{LocalPath: `C:\notes\a.md`, RemoteKey: "notes/a.md", SyncTime: now})
	s.Upsert(FileState{LocalPath: `C:\notes\b.md`, RemoteKey: "notes/b.md", SyncTime: now})
	s.Upsert(FileState{LocalPath: `C:\other\c.md`, RemoteKey: "other/c.md", SyncTime: now})

	files, err := s.AllRemoteByPrefix("notes/")
	if err != nil {
		t.Fatalf("AllRemoteByPrefix: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2, got %d", len(files))
	}
}
