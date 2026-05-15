package app

import (
	"log"
	"sync"

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
	}

	// Initialize watcher
	a.watcher, err = watcher.New()
	if err != nil {
		log.Fatalf("Cannot create watcher: %v", err)
	}

	// Start tray (blocks until exit)
	a.tray = tray.New(a.cfg, a.onTrayCommand)
	a.tray.Run()
}

func (a *App) onTrayCommand(cmd tray.Command, data any) {
	a.mu.Lock()
	defer a.mu.Unlock()

	switch cmd {
	case tray.CmdSyncNow:
		if a.syncer != nil {
			a.syncer.TriggerSync()
		}

	case tray.CmdSetMode:
		mode, ok := data.(config.SyncMode)
		if !ok {
			return
		}
		a.cfg.SetSyncMode(mode)
		a.cfg.Save()
		if mode == config.SyncModeAuto && a.syncer != nil {
			a.syncer.Start()
		}

	case tray.CmdSetWorkDirs:
		dirs, ok := data.([]config.WorkDir)
		if !ok {
			return
		}
		a.cfg.SetWorkDirs(dirs)
		a.cfg.Save()

		wd := make(map[string]string)
		for _, d := range dirs {
			wd[d.LocalPath] = d.RemotePath
		}
		a.watcher.SetWorkDirs(wd)

	case tray.CmdSetS3Config:
		s3cfg, ok := data.(config.S3Config)
		if !ok {
			return
		}
		a.cfg.SetS3Config(s3cfg)
		a.cfg.Save()

		client, err := s3client.New(s3cfg)
		if err != nil {
			log.Printf("S3 client reinit failed: %v", err)
			return
		}
		a.s3c = client

		if a.syncer == nil {
			a.syncer = syncer.New(a.cfg, a.s3c, a.st)
		}

	case tray.CmdToggleAutoStart:
		newVal := !a.cfg.AutoStart
		a.cfg.SetAutoStart(newVal)
		a.cfg.Save()
		if err := setAutoStart(newVal); err != nil {
			log.Printf("auto-start toggle failed: %v", err)
		}

	case tray.CmdQuit:
		if a.syncer != nil {
			a.syncer.Stop()
		}
		a.watcher.Stop()
		a.tray.Quit()
	}
}
