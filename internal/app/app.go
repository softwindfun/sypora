package app

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"sypora/internal/config"
	"sypora/internal/s3client"
	"sypora/internal/store"
	"sypora/internal/syncer"
	"sypora/internal/tray"
	"sypora/internal/watcher"
)

type App struct {
	cfg     *config.Config
	s3c     *s3client.Client
	st      *store.Store
	syncer  *syncer.Syncer
	watcher *watcher.Watcher
	tray    *tray.Tray

	mu sync.Mutex
}

func New() *App {
	return &App{}
}

func (a *App) Run() {
	var err error

	// Load config
	a.cfg, err = config.Load()
	if err != nil {
		log.Fatalf("Cannot load config: %v", err)
	}

	// Open store
	a.st, err = store.Open()
	if err != nil {
		log.Fatalf("Cannot open store: %v", err)
	}
	defer a.st.Close()

	// Initialize S3 client if configured
	if a.cfg.HasS3Config() {
		a.s3c, err = s3client.New(a.cfg.GetS3Config())
		if err != nil {
			log.Printf("S3 client init failed: %v", err)
		}
	}

	// Initialize syncer
	if a.s3c != nil {
		a.syncer = syncer.New(a.cfg, a.s3c, a.st)
		// Start auto-sync if configured
		if a.cfg.GetSyncMode() == config.SyncModeAuto {
			a.syncer.Start()
		}
	}

	// Initialize watcher and register initial work dirs
	a.watcher, err = watcher.New()
	if err != nil {
		log.Fatalf("Cannot create watcher: %v", err)
	}
	if dirs := a.cfg.GetWorkDirs(); len(dirs) > 0 {
		wd := make(map[string]string)
		for _, d := range dirs {
			wd[d.LocalPath] = d.RemotePath
		}
		a.watcher.SetWorkDirs(wd)
	}

	// Wire watcher events to trigger incremental syncs
	go a.watchLoop()

	// Start tray (blocks until exit)
	a.tray = tray.New(a.cfg, a.onTrayCommand)
	a.tray.Run()
}

func (a *App) watchLoop() {
	a.watcher.Start()
	for {
		select {
		case ev, ok := <-a.watcher.Events:
			if !ok {
				return
			}
			_ = ev
			if a.syncer != nil && a.cfg.GetSyncMode() == config.SyncModeAuto {
				a.syncer.TriggerSync()
			}
		case err, ok := <-a.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("watcher error: %v", err)
		}
	}
}

func (a *App) onTrayCommand(cmd tray.Command, data any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	switch cmd {
	case tray.CmdSyncNow:
		if a.syncer != nil {
			a.syncer.TriggerSync()
		}
		return nil

	case tray.CmdSetMode:
		mode, ok := data.(config.SyncMode)
		if !ok {
			return fmt.Errorf("invalid data type for set_mode")
		}
		a.cfg.SetSyncMode(mode)
		if err := a.cfg.Save(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		if mode == config.SyncModeAuto && a.syncer != nil {
			a.syncer.Start()
		}
		return nil

	case tray.CmdSetWorkDirs:
		dirs, ok := data.([]config.WorkDir)
		if !ok {
			return fmt.Errorf("invalid data type for set_work_dirs")
		}
		a.cfg.SetWorkDirs(dirs)
		if err := a.cfg.Save(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		wd := make(map[string]string)
		for _, d := range dirs {
			wd[d.LocalPath] = d.RemotePath
		}
		a.watcher.SetWorkDirs(wd)
		return nil

	case tray.CmdSetS3Config:
		s3cfg, ok := data.(config.S3Config)
		if !ok {
			return fmt.Errorf("invalid data type for set_s3_config")
		}
		a.cfg.SetS3Config(s3cfg)
		if err := a.cfg.Save(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		client, err := s3client.New(s3cfg)
		if err != nil {
			return fmt.Errorf("s3 client init: %w", err)
		}
		a.s3c = client

		if a.syncer == nil {
			a.syncer = syncer.New(a.cfg, a.s3c, a.st)
		}
		return nil

	case tray.CmdToggleAutoStart:
		newVal := !a.cfg.AutoStart
		a.cfg.SetAutoStart(newVal)
		if err := a.cfg.Save(); err != nil {
			log.Printf("save config: %v", err)
		}
		if err := setAutoStart(newVal); err != nil {
			log.Printf("auto-start toggle failed: %v", err)
		}
		return nil

	case tray.CmdExportConfig:
		filePath, ok := data.(string)
		if !ok {
			return fmt.Errorf("invalid data type for export_config")
		}
		jsonData, err := a.cfg.ExportJSON()
		if err != nil {
			return fmt.Errorf("marshal config: %w", err)
		}
		if err := os.WriteFile(filePath, jsonData, 0600); err != nil {
			return fmt.Errorf("write file: %w", err)
		}
		log.Printf("config exported to %s", filePath)
		return nil

	case tray.CmdImportConfig:
		filePath, ok := data.(string)
		if !ok {
			return fmt.Errorf("invalid data type for import_config")
		}
		jsonData, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}
		if err := a.cfg.ImportFromJSON(jsonData); err != nil {
			return fmt.Errorf("parse config: %w", err)
		}
		if err := a.cfg.Save(); err != nil {
			return fmt.Errorf("save config: %w", err)
		}

		if a.syncer != nil {
			a.syncer.Stop()
			a.syncer = nil
		}

		a.s3c = nil
		if a.cfg.HasS3Config() {
			client, err := s3client.New(a.cfg.GetS3Config())
			if err != nil {
				return fmt.Errorf("s3 client init: %w", err)
			}
			a.s3c = client
		}

		if a.s3c != nil {
			a.syncer = syncer.New(a.cfg, a.s3c, a.st)
			if a.cfg.GetSyncMode() == config.SyncModeAuto {
				a.syncer.Start()
			}
		}

		dirs := a.cfg.GetWorkDirs()
		wd := make(map[string]string)
		for _, d := range dirs {
			wd[d.LocalPath] = d.RemotePath
		}
		a.watcher.SetWorkDirs(wd)

		if err := setAutoStart(a.cfg.AutoStart); err != nil {
			log.Printf("import config: auto-start update failed: %v", err)
		}
		log.Printf("config imported from %s", filePath)
		return nil

	case tray.CmdShowVersion:
		return nil

	case tray.CmdSubmitFeedback:
		text, ok := data.(string)
		if !ok {
			return fmt.Errorf("invalid data type for submit_feedback")
		}
		dir, err := config.ConfigDir()
		if err != nil {
			return fmt.Errorf("config dir: %w", err)
		}
		filename := fmt.Sprintf("feedback_%s.txt", time.Now().Format("20060102_150405"))
		path := filepath.Join(dir, filename)
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			return fmt.Errorf("write feedback: %w", err)
		}
		log.Printf("feedback saved to %s", path)
		return nil

	case tray.CmdCheckUpdate:
		// Placeholder — will be implemented in a future iteration
		return nil

	case tray.CmdQuit:
		if a.syncer != nil {
			a.syncer.Stop()
		}
		a.watcher.Stop()
		a.tray.Quit()
		return nil
	}
	return nil
}
