package tray

import (
	"log"
	"os"
	"path/filepath"

	"sypora/internal/config"

	"github.com/getlantern/systray"
	"github.com/ncruces/zenity"
)

type CommandHandler func(cmd Command, data any)

// Re-export from app for tray to use
type Command string

const (
	CmdSyncNow        Command = "sync_now"
	CmdSetMode                = "set_mode"
	CmdSetWorkDirs            = "set_work_dirs"
	CmdSetS3Config            = "set_s3_config"
	CmdToggleAutoStart        = "toggle_auto_start"
	CmdQuit                   = "quit"
)

type Tray struct {
	cfg     *config.Config
	handler CommandHandler

	mStatus      *systray.MenuItem
	mSyncNow     *systray.MenuItem
	mAutoSync    *systray.MenuItem
	mManualSync  *systray.MenuItem
	mWorkDirs    *systray.MenuItem
	mS3Config    *systray.MenuItem
	mAutoStart   *systray.MenuItem
	mQuit        *systray.MenuItem
}

func New(cfg *config.Config, handler CommandHandler) *Tray {
	return &Tray{cfg: cfg, handler: handler}
}

func (t *Tray) Run() {
	systray.Run(t.onReady, t.onExit)
}

func (t *Tray) Quit() {
	systray.Quit()
}

func (t *Tray) onReady() {
	iconData, err := loadIcon()
	if err != nil {
		log.Printf("Cannot load icon: %v", err)
	}
	systray.SetIcon(iconData)
	systray.SetTitle("Sypora")
	systray.SetTooltip("Sypora - S3 Note Sync")

	t.mStatus = systray.AddMenuItem("等待同步...", "")
	t.mStatus.Disable()
	systray.AddSeparator()

	t.mSyncNow = systray.AddMenuItem("立即同步", "")
	systray.AddSeparator()

	modeMenu := systray.AddMenuItem("同步模式", "")
	t.mAutoSync = modeMenu.AddSubMenuItem("自动同步", "")
	t.mManualSync = modeMenu.AddSubMenuItem("手动同步", "")
	systray.AddSeparator()

	t.mWorkDirs = systray.AddMenuItem("工作目录设置...", "")
	t.mS3Config = systray.AddMenuItem("S3 服务器配置...", "")
	systray.AddSeparator()

	t.mAutoStart = systray.AddMenuItem("开机自启", "")
	t.updateAutoStartCheck()
	systray.AddSeparator()

	systray.AddMenuItem("关于 Sypora", "").Disable()
	t.mQuit = systray.AddMenuItem("退出", "")

	// Set initial check marks
	t.updateModeChecks()

	go t.handleEvents()
}

func (t *Tray) onExit() {
	// Cleanup if needed
}

func (t *Tray) handleEvents() {
	for {
		select {
		case <-t.mSyncNow.ClickedCh:
			t.handler(CmdSyncNow, nil)

		case <-t.mAutoSync.ClickedCh:
			t.handler(CmdSetMode, config.SyncModeAuto)
			t.updateModeChecks()

		case <-t.mManualSync.ClickedCh:
			t.handler(CmdSetMode, config.SyncModeManual)
			t.updateModeChecks()

		case <-t.mWorkDirs.ClickedCh:
			t.handleWorkDirsConfig()

		case <-t.mS3Config.ClickedCh:
			t.handleS3Config()

		case <-t.mAutoStart.ClickedCh:
			t.handler(CmdToggleAutoStart, nil)
			t.updateAutoStartCheck()

		case <-t.mQuit.ClickedCh:
			t.handler(CmdQuit, nil)
			return
		}
	}
}

func (t *Tray) handleWorkDirsConfig() {
	dirs := t.cfg.GetWorkDirs()

	for {
		// Build list of current dirs for display
		var items []string
		for _, d := range dirs {
			items = append(items, d.LocalPath+" → "+d.RemotePath)
		}
		items = append(items, "+ 添加新目录")
		if len(dirs) > 0 {
			items = append(items, "- 移出最后一个目录")
		}

		choice, err := zenity.List("管理工作的目录：", items,
			zenity.Title("Sypora - 工作目录"),
		)
		if err != nil {
			break
		}

		switch choice {
		case "+ 添加新目录":
			folder, err := zenity.SelectFile(
				zenity.Title("选择工作目录"),
				zenity.Directory(),
			)
			if err != nil || folder == "" {
				continue
			}

			remotePrefix, err := zenity.Entry("输入 S3 上对应的前缀（如 notes/）：",
				zenity.Title("Sypora - 远程路径"),
				zenity.EntryText(folder+"_backup"),
			)
			if err != nil || remotePrefix == "" {
				continue
			}

			dirs = append(dirs, config.WorkDir{
				LocalPath:  folder,
				RemotePath: remotePrefix,
			})

		case "- 移出最后一个目录":
			if len(dirs) > 0 {
				dirs = dirs[:len(dirs)-1]
			}
		}
	}

	t.handler(CmdSetWorkDirs, dirs)
}

func (t *Tray) handleS3Config() {
	current := t.cfg.GetS3Config()

	endpoint, err := zenity.Entry("S3 服务器地址（如 s3.amazonaws.com 或 127.0.0.1:9000）：",
		zenity.Title("Sypora - S3 配置"),
		zenity.EntryText(current.Endpoint),
	)
	if err != nil {
		return
	}

	accessKey, err := zenity.Entry("Access Key：",
		zenity.Title("Sypora - S3 配置"),
		zenity.EntryText(current.AccessKey),
	)
	if err != nil {
		return
	}

	secretKey, err := zenity.Entry("Secret Key：",
		zenity.Title("Sypora - S3 配置"),
		zenity.EntryText(current.SecretKey),
	)
	if err != nil {
		return
	}

	bucket, err := zenity.Entry("Bucket 名称：",
		zenity.Title("Sypora - S3 配置"),
		zenity.EntryText(current.Bucket),
	)
	if err != nil {
		return
	}

	region, err := zenity.Entry("Region（如 us-east-1，MinIO 可为空）：",
		zenity.Title("Sypora - S3 配置"),
		zenity.EntryText(current.Region),
	)
	if err != nil {
		return
	}

	err = zenity.Question("使用 HTTPS/SSL 连接？",
		zenity.Title("Sypora - S3 配置"),
		zenity.OKLabel("是"),
		zenity.CancelLabel("否"),
	)
	useSSL := (err == nil)

	t.handler(CmdSetS3Config, config.S3Config{
		Endpoint:  endpoint,
		AccessKey: accessKey,
		SecretKey: secretKey,
		Bucket:    bucket,
		Region:    region,
		UseSSL:    useSSL,
	})
}

func (t *Tray) updateModeChecks() {
	mode := t.cfg.GetSyncMode()
	if mode == config.SyncModeAuto {
		t.mAutoSync.Check()
		t.mManualSync.Uncheck()
	} else {
		t.mAutoSync.Uncheck()
		t.mManualSync.Check()
	}
}

func (t *Tray) updateAutoStartCheck() {
	if t.cfg.AutoStart {
		t.mAutoStart.Check()
	} else {
		t.mAutoStart.Uncheck()
	}
}

func loadIcon() ([]byte, error) {
	// Try embedded icon first, then fall back to file
	paths := []string{
		"assets/icon.ico",
		filepath.Join(filepath.Dir(os.Args[0]), "assets/icon.ico"),
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err == nil {
			return data, nil
		}
	}
	// Return a minimal 1x1 ICO as fallback
	return minimalICO, nil
}

// minimalICO is a minimal valid ICO file (1x1 transparent pixel)
var minimalICO = []byte{
	0, 0, 1, 0, 1, 0, 0, 0, 0, 0, 1, 0, 32, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0,
}
