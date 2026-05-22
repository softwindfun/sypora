package syncer

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"sypora/internal/config"
	"sypora/internal/s3client"
	"sypora/internal/store"
)

type mockS3 struct {
	objects map[string]s3client.ObjectInfo
}

func newMockS3(objs map[string]s3client.ObjectInfo) *mockS3 {
	if objs == nil {
		objs = make(map[string]s3client.ObjectInfo)
	}
	return &mockS3{objects: objs}
}

func (m *mockS3) ListObjects(prefix string) ([]s3client.ObjectInfo, error) {
	var result []s3client.ObjectInfo
	for k, v := range m.objects {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			result = append(result, v)
		}
	}
	return result, nil
}

func (m *mockS3) Upload(localPath, remoteKey string) (string, error) {
	return "mock-etag", nil
}

func (m *mockS3) Download(remoteKey, localPath string) error {
	os.MkdirAll(filepath.Dir(localPath), 0755)
	return os.WriteFile(localPath, []byte("mock-content"), 0644)
}

func (m *mockS3) DeleteObject(remoteKey string) error {
	delete(m.objects, remoteKey)
	return nil
}

func (m *mockS3) ObjectInfo(remoteKey string) (*s3client.ObjectInfo, error) {
	if info, ok := m.objects[remoteKey]; ok {
		return &info, nil
	}
	return nil, nil
}

func setupPlanTest(t *testing.T) (string, *store.Store, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	t.Setenv("APPDATA", tmpDir)

	st, err := store.Open()
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}

	cleanup := func() {
		st.Close()
	}
	return tmpDir, st, cleanup
}

func TestBuildPlanNewLocalFile(t *testing.T) {
	tmpDir, st, cleanup := setupPlanTest(t)
	defer cleanup()

	// Create a local file
	localDir := filepath.Join(tmpDir, "notes")
	os.MkdirAll(localDir, 0755)
	os.WriteFile(filepath.Join(localDir, "new.md"), []byte("hello"), 0644)

	wd := config.WorkDir{
		LocalPath:  localDir,
		RemotePath: "notes/",
	}

	mock := newMockS3(nil)

	plan, err := buildPlan(wd, mock, st)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}

	if len(plan.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(plan.Actions))
	}
	action := plan.Actions[0]
	if action.Type != "upload" {
		t.Fatalf("expected upload, got %s", action.Type)
	}
	if action.RemoteKey != "notes/new.md" {
		t.Fatalf("expected remote key notes/new.md, got %s", action.RemoteKey)
	}
}

func TestBuildPlanNewRemoteFile(t *testing.T) {
	tmpDir, st, cleanup := setupPlanTest(t)
	defer cleanup()

	localDir := filepath.Join(tmpDir, "notes")
	os.MkdirAll(localDir, 0755)

	wd := config.WorkDir{
		LocalPath:  localDir,
		RemotePath: "notes/",
	}

	now := time.Now()
	mock := newMockS3(map[string]s3client.ObjectInfo{
		"notes/remote.md": {
			Key:          "notes/remote.md",
			Size:         100,
			ETag:         "abc",
			LastModified: now,
		},
	})

	plan, err := buildPlan(wd, mock, st)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}

	if len(plan.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d: %+v", len(plan.Actions), plan.Actions)
	}
	action := plan.Actions[0]
	if action.Type != "download" {
		t.Fatalf("expected download, got %s", action.Type)
	}
}

func TestBuildPlanNoChanges(t *testing.T) {
	tmpDir, st, cleanup := setupPlanTest(t)
	defer cleanup()

	localDir := filepath.Join(tmpDir, "notes")
	os.MkdirAll(localDir, 0755)
	localFile := filepath.Join(localDir, "same.md")
	if err := os.WriteFile(localFile, []byte("same content"), 0644); err != nil {
		t.Fatal(err)
	}

	info, _ := os.Stat(localFile)
	now := time.Now()

	wd := config.WorkDir{
		LocalPath:  localDir,
		RemotePath: "notes/",
	}

	mock := newMockS3(map[string]s3client.ObjectInfo{
		"notes/same.md": {
			Key:          "notes/same.md",
			Size:         info.Size(),
			ETag:         "known-etag",
			LastModified: now,
		},
	})

	// Pre-populate state - synced previously
	st.Upsert(store.FileState{
		LocalPath:   localFile,
		RemoteKey:   "notes/same.md",
		LocalMTime:  info.ModTime(),
		LocalSize:   info.Size(),
		RemoteETag:  "known-etag",
		RemoteMTime: now,
		RemoteSize:  info.Size(),
		SyncTime:    now,
	})

	plan, err := buildPlan(wd, mock, st)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}

	if len(plan.Actions) != 0 {
		t.Fatalf("expected 0 actions for unchanged files, got %d: %+v", len(plan.Actions), plan.Actions)
	}
}

func TestBuildPlanConflict(t *testing.T) {
	tmpDir, st, cleanup := setupPlanTest(t)
	defer cleanup()

	localDir := filepath.Join(tmpDir, "notes")
	os.MkdirAll(localDir, 0755)

	// Write local file, then wait a bit so mtime is noticeably later
	localFile := filepath.Join(localDir, "conflict.md")
	os.WriteFile(localFile, []byte("local version"), 0644)

	// Get file info after write
	info, _ := os.Stat(localFile)
	oldMtime := info.ModTime()
	oldSize := info.Size()

	now := time.Now()
	oldEtag := "old-etag"

	wd := config.WorkDir{
		LocalPath:  localDir,
		RemotePath: "notes/",
	}

	mock := newMockS3(map[string]s3client.ObjectInfo{
		"notes/conflict.md": {
			Key:          "notes/conflict.md",
			Size:         200,
			ETag:         "new-etag",
			LastModified: now,
		},
	})

	// Pre-populate state with OLD values
	st.Upsert(store.FileState{
		LocalPath:   localFile,
		RemoteKey:   "notes/conflict.md",
		LocalMTime:  oldMtime,
		LocalSize:   oldSize,
		RemoteETag:  oldEtag,
		RemoteMTime: now.Add(-1 * time.Hour),
		RemoteSize:  100,
		SyncTime:    now.Add(-1 * time.Hour),
	})

	// Modify local file after the stored state
	time.Sleep(100 * time.Millisecond)
	os.WriteFile(localFile, []byte("local modified"), 0644)

	plan, err := buildPlan(wd, mock, st)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}

	hasConflict := false
	for _, a := range plan.Actions {
		if a.Type == "conflict" {
			hasConflict = true
		}
	}
	if !hasConflict {
		t.Fatalf("expected conflict action, got: %+v", plan.Actions)
	}
}

func TestBuildPlanLocalDelete(t *testing.T) {
	tmpDir, st, cleanup := setupPlanTest(t)
	defer cleanup()

	localDir := filepath.Join(tmpDir, "notes")
	os.MkdirAll(localDir, 0755)

	now := time.Now()
	wd := config.WorkDir{
		LocalPath:  localDir,
		RemotePath: "notes/",
	}

	mock := newMockS3(map[string]s3client.ObjectInfo{
		"notes/deleted.md": {
			Key:          "notes/deleted.md",
			Size:         100,
			ETag:         "etag",
			LastModified: now,
		},
	})

	// Pre-populate state — local file was previously synced
	st.Upsert(store.FileState{
		LocalPath:   filepath.Join(localDir, "deleted.md"),
		RemoteKey:   "notes/deleted.md",
		RemoteETag:  "etag",
		RemoteMTime: now,
		RemoteSize:  100,
		SyncTime:    now,
	})

	plan, err := buildPlan(wd, mock, st)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}

	// Remote exists, local doesn't, and remote hasn't changed
	// -> should delete remote
	hasDeleteRemote := false
	for _, a := range plan.Actions {
		if a.Type == "delete_remote" {
			hasDeleteRemote = true
		}
	}
	if !hasDeleteRemote {
		t.Fatalf("expected delete_remote action, got: %+v", plan.Actions)
	}
}

func TestBuildPlanLocalChangedRemoteUnchanged(t *testing.T) {
	tmpDir, st, cleanup := setupPlanTest(t)
	defer cleanup()

	localDir := filepath.Join(tmpDir, "notes")
	os.MkdirAll(localDir, 0755)
	localFile := filepath.Join(localDir, "changed.md")
	os.WriteFile(localFile, []byte("original"), 0644)
	origInfo, _ := os.Stat(localFile)

	now := time.Now()
	wd := config.WorkDir{
		LocalPath:  localDir,
		RemotePath: "notes/",
	}

	mock := newMockS3(map[string]s3client.ObjectInfo{
		"notes/changed.md": {
			Key:          "notes/changed.md",
			Size:         100,
			ETag:         "etag",
			LastModified: now,
		},
	})

	// State from previous sync
	st.Upsert(store.FileState{
		LocalPath:   localFile,
		RemoteKey:   "notes/changed.md",
		LocalMTime:  origInfo.ModTime(),
		LocalSize:   origInfo.Size(),
		RemoteETag:  "etag",
		RemoteMTime: now,
		RemoteSize:  100,
		SyncTime:    now,
	})

	// Modify local file after recording state
	time.Sleep(100 * time.Millisecond)
	os.WriteFile(localFile, []byte("modified local content"), 0644)

	plan, err := buildPlan(wd, mock, st)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}

	hasUpload := false
	for _, a := range plan.Actions {
		if a.Type == "upload" {
			hasUpload = true
		}
	}
	if !hasUpload {
		t.Fatalf("expected upload for locally changed file, got: %+v", plan.Actions)
	}
}

func TestBuildPlanRemoteChangedLocalUnchanged(t *testing.T) {
	tmpDir, st, cleanup := setupPlanTest(t)
	defer cleanup()

	localDir := filepath.Join(tmpDir, "notes")
	os.MkdirAll(localDir, 0755)
	localFile := filepath.Join(localDir, "remote_changed.md")
	os.WriteFile(localFile, []byte("local content"), 0644)
	localInfo, _ := os.Stat(localFile)

	now := time.Now()
	wd := config.WorkDir{
		LocalPath:  localDir,
		RemotePath: "notes/",
	}

	mock := newMockS3(map[string]s3client.ObjectInfo{
		"notes/remote_changed.md": {
			Key:          "notes/remote_changed.md",
			Size:         200,
			ETag:         "new-etag",
			LastModified: now,
		},
	})

	// State from previous sync
	st.Upsert(store.FileState{
		LocalPath:   localFile,
		RemoteKey:   "notes/remote_changed.md",
		LocalMTime:  localInfo.ModTime(),
		LocalSize:   localInfo.Size(),
		RemoteETag:  "old-etag",
		RemoteMTime: now.Add(-1 * time.Hour),
		RemoteSize:  100,
		SyncTime:    now.Add(-1 * time.Hour),
	})

	plan, err := buildPlan(wd, mock, st)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}

	hasDownload := false
	for _, a := range plan.Actions {
		if a.Type == "download" {
			hasDownload = true
		}
	}
	if !hasDownload {
		t.Fatalf("expected download for remote-changed file, got: %+v", plan.Actions)
	}
}

func TestBuildPlanMultipleWorkDirs(t *testing.T) {
	// Each workdir is processed independently. Verify that files under one
	// directory don't leak into another's plan.
	tmpDir, st, cleanup := setupPlanTest(t)
	defer cleanup()

	dir1 := filepath.Join(tmpDir, "notes1")
	os.MkdirAll(dir1, 0755)
	os.WriteFile(filepath.Join(dir1, "a.md"), []byte("a"), 0644)

	dir2 := filepath.Join(tmpDir, "notes2")
	os.MkdirAll(dir2, 0755)
	os.WriteFile(filepath.Join(dir2, "b.md"), []byte("b"), 0644)

	// Build plan for dir1 — mock has no remote objects
	plan1, err := buildPlan(
		config.WorkDir{LocalPath: dir1, RemotePath: "notes1/"},
		newMockS3(nil), st,
	)
	if err != nil {
		t.Fatalf("buildPlan dir1: %v", err)
	}

	// Build plan for dir2
	plan2, err := buildPlan(
		config.WorkDir{LocalPath: dir2, RemotePath: "notes2/"},
		newMockS3(nil), st,
	)
	if err != nil {
		t.Fatalf("buildPlan dir2: %v", err)
	}

	if len(plan1.Actions) != 1 {
		t.Fatalf("dir1: expected 1 action, got %d", len(plan1.Actions))
	}
	if len(plan2.Actions) != 1 {
		t.Fatalf("dir2: expected 1 action, got %d", len(plan2.Actions))
	}
}

func TestBuildPlanIgnoresHiddenFiles(t *testing.T) {
	tmpDir, st, cleanup := setupPlanTest(t)
	defer cleanup()

	localDir := filepath.Join(tmpDir, "notes")
	os.MkdirAll(localDir, 0755)
	os.MkdirAll(filepath.Join(localDir, ".hidden_dir"), 0755)
	os.WriteFile(filepath.Join(localDir, ".hidden"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(localDir, "visible.md"), []byte("v"), 0644)

	wd := config.WorkDir{
		LocalPath:  localDir,
		RemotePath: "notes/",
	}

	plan, err := buildPlan(wd, newMockS3(nil), st)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}

	// Should only have 1 action (for visible.md), not .hidden
	if len(plan.Actions) != 1 {
		t.Fatalf("expected 1 action (visible only), got %d: %+v", len(plan.Actions), plan.Actions)
	}
}

func TestBuildPlanRemoteDeleteUnchangedLocal(t *testing.T) {
	tmpDir, st, cleanup := setupPlanTest(t)
	defer cleanup()

	localDir := filepath.Join(tmpDir, "notes")
	os.MkdirAll(localDir, 0755)
	localFile := filepath.Join(localDir, "stale.md")
	os.WriteFile(localFile, []byte("stale"), 0644)
	info, _ := os.Stat(localFile)

	now := time.Now()
	wd := config.WorkDir{
		LocalPath:  localDir,
		RemotePath: "notes/",
	}

	// No remote objects (remote was deleted)
	mock := newMockS3(nil)

	// Previously synced state
	st.Upsert(store.FileState{
		LocalPath:   localFile,
		RemoteKey:   "notes/stale.md",
		LocalMTime:  info.ModTime(),
		LocalSize:   info.Size(),
		RemoteETag:  "old-etag",
		RemoteMTime: now.Add(-1 * time.Hour),
		RemoteSize:  100,
		SyncTime:    now.Add(-1 * time.Hour),
	})

	plan, err := buildPlan(wd, mock, st)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}

	hasDeleteLocal := false
	for _, a := range plan.Actions {
		if a.Type == "delete_local" {
			hasDeleteLocal = true
		}
	}
	if !hasDeleteLocal {
		t.Fatalf("expected delete_local action for stale local file, got: %+v", plan.Actions)
	}
}

// Helper to print actions for debugging
func actionTypes(actions []SyncAction) []string {
	var types []string
	for _, a := range actions {
		types = append(types, a.Type+":"+a.Reason)
	}
	sort.Strings(types)
	return types
}
