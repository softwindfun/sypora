package syncer

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"sypora/internal/config"
	"sypora/internal/s3client"
	"sypora/internal/store"
)

type SyncAction struct {
	Type      string // "upload", "download", "delete_local", "delete_remote", "conflict"
	LocalPath string
	RemoteKey string
	Reason    string
}

type Plan struct {
	Actions []SyncAction
}

// SyncerS3 describes the S3 operations needed by the sync engine.
type SyncerS3 interface {
	ListObjects(prefix string) ([]s3client.ObjectInfo, error)
	Upload(localPath, remoteKey string) (string, error)
	Download(remoteKey, localPath string) error
	DeleteObject(remoteKey string) error
	ObjectInfo(remoteKey string) (*s3client.ObjectInfo, error)
}

func buildPlan(wd config.WorkDir, s3c SyncerS3, st *store.Store) (*Plan, error) {
	plan := &Plan{}

	// Scan local files
	localFiles, err := scanLocal(wd.LocalPath)
	if err != nil {
		return nil, err
	}

	// Scan remote objects
	remotePrefix := strings.TrimSuffix(wd.RemotePath, "/")
	if remotePrefix != "" {
		remotePrefix += "/"
	}
	remoteObjects, err := s3c.ListObjects(remotePrefix)
	if err != nil {
		return nil, err
	}

	// Index by relative path
	localByRel := make(map[string]localFileInfo)
	for _, f := range localFiles {
		localByRel[f.RelPath] = f
	}

	remoteByRel := make(map[string]s3client.ObjectInfo)
	for _, obj := range remoteObjects {
		rel := strings.TrimPrefix(obj.Key, remotePrefix)
		if rel == "" || strings.HasSuffix(rel, "/") {
			continue // skip directory markers
		}
		remoteByRel[rel] = obj
	}

	// Load known state
	dbStates, err := st.AllByPrefix(wd.LocalPath + string(filepath.Separator))
	if err != nil {
		return nil, err
	}
	dbByRel := make(map[string]store.FileState)
	for _, fs := range dbStates {
		rel, err := filepath.Rel(wd.LocalPath, fs.LocalPath)
		if err != nil {
			continue
		}
		dbByRel[filepath.ToSlash(rel)] = fs
	}

	now := time.Now()

	// Compare: local vs known state vs remote
	for rel, local := range localByRel {
		remote, remoteExists := remoteByRel[rel]
		db, dbExists := dbByRel[rel]

		if !remoteExists {
			if !dbExists {
				// New local file, never synced -> upload
				fs := makeFileState(wd.LocalPath, wd.RemotePath, rel, local)
				plan.Actions = append(plan.Actions, SyncAction{
					Type: "upload", LocalPath: local.FullPath,
					RemoteKey: fs.RemoteKey, Reason: "new local file",
				})
			} else if truncateTime(local.ModTime).After(truncateTime(db.LocalMTime)) {
				// Local modified, remote deleted -> re-upload
				fs := makeFileState(wd.LocalPath, wd.RemotePath, rel, local)
				plan.Actions = append(plan.Actions, SyncAction{
					Type: "upload", LocalPath: local.FullPath,
					RemoteKey: fs.RemoteKey, Reason: "local modified, remote deleted",
				})
			} else {
				// Remote deleted, local unchanged -> delete local
				plan.Actions = append(plan.Actions, SyncAction{
					Type: "delete_local", LocalPath: local.FullPath,
					RemoteKey: db.RemoteKey, Reason: "remote deleted",
				})
			}
			continue
		}

		remoteKey := remotePrefix + rel

		if !dbExists {
			// New to us — check if both exist (conflict) or just local is new
			plan.Actions = append(plan.Actions, SyncAction{
				Type: "upload", LocalPath: local.FullPath,
				RemoteKey: remoteKey, Reason: "new file, upload",
			})
			continue
		}

		localChanged := truncateTime(local.ModTime).After(truncateTime(db.LocalMTime)) || local.Size != db.LocalSize
		remoteChanged := remote.ETag != db.RemoteETag || remote.Size != db.RemoteSize

		if localChanged && remoteChanged {
			plan.Actions = append(plan.Actions, SyncAction{
				Type: "conflict", LocalPath: local.FullPath,
				RemoteKey: remoteKey, Reason: "both changed",
			})
		} else if localChanged {
			plan.Actions = append(plan.Actions, SyncAction{
				Type: "upload", LocalPath: local.FullPath,
				RemoteKey: remoteKey, Reason: "local changed",
			})
		} else if remoteChanged {
			plan.Actions = append(plan.Actions, SyncAction{
				Type: "download", LocalPath: local.FullPath,
				RemoteKey: remoteKey, Reason: "remote changed",
			})
		}

		// Mark as handled
		delete(remoteByRel, rel)
	}

	// Remaining remote objects (not in local)
	for rel, remote := range remoteByRel {
		remoteKey := remotePrefix + rel
		localFullPath := filepath.Join(wd.LocalPath, filepath.FromSlash(rel))
		db, dbExists := dbByRel[rel]

		if dbExists && remote.ETag == db.RemoteETag && remote.Size == db.RemoteSize {
			// Remote unchanged, local deleted -> delete remote
			plan.Actions = append(plan.Actions, SyncAction{
				Type: "delete_remote", LocalPath: localFullPath,
				RemoteKey: remoteKey, Reason: "local deleted",
			})
		} else {
			// Remote new or changed, local missing -> download
			plan.Actions = append(plan.Actions, SyncAction{
				Type: "download", LocalPath: localFullPath,
				RemoteKey: remoteKey, Reason: "new remote file",
			})
		}
	}

	_ = now
	return plan, nil
}

type localFileInfo struct {
	FullPath string
	RelPath  string
	ModTime  time.Time
	Size     int64
	IsDir    bool
}

func scanLocal(root string) ([]localFileInfo, error) {
	var files []localFileInfo
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		relPath := filepath.ToSlash(rel)
		if shouldIgnorePath(relPath) {
			return nil
		}
		files = append(files, localFileInfo{
			FullPath: path,
			RelPath:  relPath,
			ModTime:  info.ModTime(),
			Size:     info.Size(),
		})
		return nil
	})
	return files, err
}

func makeFileState(localRoot, remotePrefix, relPath string, f localFileInfo) store.FileState {
	remoteKey := strings.TrimSuffix(remotePrefix, "/") + "/" + relPath
	return store.FileState{
		LocalPath:  filepath.Join(localRoot, filepath.FromSlash(relPath)),
		RemoteKey:  remoteKey,
		LocalMTime: f.ModTime,
		LocalSize:  f.Size,
	}
}

func truncateTime(t time.Time) time.Time {
	return t.Truncate(time.Second)
}

func shouldIgnorePath(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if strings.HasPrefix(p, ".") {
			return true
		}
	}
	return false
}
