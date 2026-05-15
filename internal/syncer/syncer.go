package syncer

import (
	"log"
	"os"
	"sync"
	"time"

	"sypora/internal/config"
	"sypora/internal/s3client"
	"sypora/internal/store"
)

type Status struct {
	Running    bool
	LastSync   time.Time
	LastError  error
	SyncCount  int    // files synced in last cycle
	State      string // "idle", "syncing", "error"
}

type Syncer struct {
	cfg    *config.Config
	s3c    *s3client.Client
	store  *store.Store
	status Status
	mu     sync.RWMutex

	eventCh chan struct{} // triggers a full sync
	stopCh  chan struct{}
}

func New(cfg *config.Config, s3c *s3client.Client, st *store.Store) *Syncer {
	return &Syncer{
		cfg:     cfg,
		s3c:     s3c,
		store:   st,
		eventCh: make(chan struct{}, 1),
		stopCh:  make(chan struct{}),
	}
}

func (s *Syncer) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

func (s *Syncer) TriggerSync() {
	select {
	case s.eventCh <- struct{}{}:
	default:
		// already pending
	}
}

func (s *Syncer) Start() {
	go s.loop()
}

func (s *Syncer) Stop() {
	close(s.stopCh)
}

func (s *Syncer) loop() {
	// Do an initial full sync
	s.runFullSync()

	// Periodic full sync every 5 minutes
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-s.eventCh:
			s.runFullSync()
		case <-ticker.C:
			s.runFullSync()
		}
	}
}

func (s *Syncer) runFullSync() {
	s.mu.Lock()
	s.status.State = "syncing"
	s.status.Running = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.status.State = "idle"
		s.status.LastSync = time.Now()
		s.mu.Unlock()
	}()

	workDirs := s.cfg.GetWorkDirs()
	syncCount := 0

	for _, wd := range workDirs {
		plan, err := buildPlan(wd, s.s3c, s.store)
		if err != nil {
			log.Printf("sync plan error for %s: %v", wd.LocalPath, err)
			s.mu.Lock()
			s.status.LastError = err
			s.status.State = "error"
			s.mu.Unlock()
			continue
		}

		for _, action := range plan.Actions {
			if err := s.executeAction(action); err != nil {
				log.Printf("sync action error: %v", err)
				s.mu.Lock()
				s.status.LastError = err
				s.status.State = "error"
				s.mu.Unlock()
				continue
			}
			syncCount++
		}
	}

	s.mu.Lock()
	s.status.SyncCount = syncCount
	s.status.LastError = nil
	s.mu.Unlock()
}

func (s *Syncer) executeAction(action SyncAction) error {
	switch action.Type {
	case "upload":
		return s.doUpload(action)
	case "download":
		return s.doDownload(action)
	case "delete_local":
		return s.doDeleteLocal(action)
	case "delete_remote":
		return s.doDeleteRemote(action)
	case "conflict":
		return s.doConflict(action)
	}
	return nil
}

func (s *Syncer) doUpload(action SyncAction) error {
	etag, err := s.s3c.Upload(action.LocalPath, action.RemoteKey)
	if err != nil {
		return err
	}

	info, _ := os.Stat(action.LocalPath)
	now := time.Now()
	var mtime time.Time
	var size int64
	if info != nil {
		mtime = info.ModTime()
		size = info.Size()
	}

	return s.store.Upsert(store.FileState{
		LocalPath:   action.LocalPath,
		RemoteKey:   action.RemoteKey,
		LocalMTime:  mtime,
		LocalSize:   size,
		RemoteETag:  etag,
		RemoteMTime: now,
		RemoteSize:  size,
		SyncTime:    now,
	})
}

func (s *Syncer) doDownload(action SyncAction) error {
	if err := s.s3c.Download(action.RemoteKey, action.LocalPath); err != nil {
		return err
	}

	// Get remote info for state tracking
	objInfo, err := s.s3c.ObjectInfo(action.RemoteKey)
	if err != nil {
		return err
	}

	info, _ := os.Stat(action.LocalPath)
	now := time.Now()
	var mtime time.Time
	var size int64
	if info != nil {
		mtime = info.ModTime()
		size = info.Size()
	}

	return s.store.Upsert(store.FileState{
		LocalPath:   action.LocalPath,
		RemoteKey:   action.RemoteKey,
		LocalMTime:  mtime,
		LocalSize:   size,
		RemoteETag:  objInfo.ETag,
		RemoteMTime: objInfo.LastModified,
		RemoteSize:  objInfo.Size,
		SyncTime:    now,
	})
}

func (s *Syncer) doDeleteLocal(action SyncAction) error {
	if err := os.Remove(action.LocalPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return s.store.Delete(action.LocalPath)
}

func (s *Syncer) doDeleteRemote(action SyncAction) error {
	if err := s.s3c.DeleteObject(action.RemoteKey); err != nil {
		return err
	}
	return s.store.DeleteByRemoteKey(action.RemoteKey)
}

func (s *Syncer) doConflict(action SyncAction) error {
	// Download the remote version as a conflict copy
	conflictLocal := conflictName(action.LocalPath)
	if err := s.s3c.Download(action.RemoteKey, conflictLocal); err != nil {
		return err
	}
	// Also update state to reflect we've synced this conflict
	objInfo, err := s.s3c.ObjectInfo(action.RemoteKey)
	if err != nil {
		return err
	}
	info, _ := os.Stat(action.LocalPath)
	now := time.Now()
	var mtime time.Time
	var size int64
	if info != nil {
		mtime = info.ModTime()
		size = info.Size()
	}
	return s.store.Upsert(store.FileState{
		LocalPath:   action.LocalPath,
		RemoteKey:   action.RemoteKey,
		LocalMTime:  mtime,
		LocalSize:   size,
		RemoteETag:  objInfo.ETag,
		RemoteMTime: objInfo.LastModified,
		RemoteSize:  objInfo.Size,
		SyncTime:    now,
	})
}
