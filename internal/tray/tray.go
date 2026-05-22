package tray

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sypora/internal/config"

	"github.com/getlantern/systray"
	"github.com/ncruces/zenity"
)

type CommandHandler func(cmd Command, data any) error

// Re-export from app for tray to use
const Version = "1.0.0"

type Command string

const (
	CmdSyncNow        Command = "sync_now"
	CmdSetMode                = "set_mode"
	CmdSetWorkDirs            = "set_work_dirs"
	CmdSetS3Config            = "set_s3_config"
	CmdToggleAutoStart        = "toggle_auto_start"
	CmdExportConfig           = "export_config"
	CmdImportConfig           = "import_config"
	CmdShowVersion            = "show_version"
	CmdSubmitFeedback         = "submit_feedback"
	CmdCheckUpdate            = "check_update"
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
	mExportCfg   *systray.MenuItem
	mImportCfg   *systray.MenuItem
	mAutoStart   *systray.MenuItem
	mVersion     *systray.MenuItem
	mFeedback    *systray.MenuItem
	mUpdate      *systray.MenuItem
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
	t.mAutoSync = modeMenu.AddSubMenuItemCheckbox("自动同步", "", true)
	t.mManualSync = modeMenu.AddSubMenuItemCheckbox("手动同步", "", false)
	systray.AddSeparator()

	t.mWorkDirs = systray.AddMenuItem("工作目录", "")
	t.mS3Config = systray.AddMenuItem("S3 配置", "")
	migrateMenu := systray.AddMenuItem("配置迁移", "")
	t.mExportCfg = migrateMenu.AddSubMenuItem("导出配置", "")
	t.mImportCfg = migrateMenu.AddSubMenuItem("导入配置", "")
	systray.AddSeparator()

	t.mAutoStart = systray.AddMenuItemCheckbox("开机自启", "", false)
	t.updateAutoStartCheck()
	systray.AddSeparator()

	aboutMenu := systray.AddMenuItem("关于", "")
	t.mVersion = aboutMenu.AddSubMenuItem("版本", "")
	t.mFeedback = aboutMenu.AddSubMenuItem("反馈", "")
	t.mUpdate = aboutMenu.AddSubMenuItem("更新", "")
	t.mQuit = systray.AddMenuItem("退出", "")

	// Set initial check marks
	t.updateModeChecks()

	go t.handleEvents()

	// Prompt for work dirs on first launch
	if !t.cfg.HasWorkDirs() {
		go func() {
			time.Sleep(500 * time.Millisecond) // let tray settle
			t.handleWorkDirsConfig()
		}()
	}
}

func (t *Tray) onExit() {
	// Cleanup if needed
}

func (t *Tray) handleEvents() {
	for {
		select {
		case <-t.mSyncNow.ClickedCh:
			_ = t.handler(CmdSyncNow, nil)

		case <-t.mAutoSync.ClickedCh:
			_ = t.handler(CmdSetMode, config.SyncModeAuto)
			t.updateModeChecks()

		case <-t.mManualSync.ClickedCh:
			_ = t.handler(CmdSetMode, config.SyncModeManual)
			t.updateModeChecks()

		case <-t.mWorkDirs.ClickedCh:
			t.handleWorkDirsConfig()

		case <-t.mS3Config.ClickedCh:
			t.handleS3Config()

		case <-t.mExportCfg.ClickedCh:
			t.handleExportConfig()

		case <-t.mImportCfg.ClickedCh:
			t.handleImportConfig()
			t.updateModeChecks()
			t.updateAutoStartCheck()

		case <-t.mAutoStart.ClickedCh:
			_ = t.handler(CmdToggleAutoStart, nil)
			t.updateAutoStartCheck()

		case <-t.mVersion.ClickedCh:
			t.handleVersion()

		case <-t.mFeedback.ClickedCh:
			t.handleFeedback()

		case <-t.mUpdate.ClickedCh:
			t.handleUpdate()

		case <-t.mQuit.ClickedCh:
			_ = t.handler(CmdQuit, nil)
			return
		}
	}
}

func (t *Tray) handleWorkDirsConfig() {
	dirs, saved := showWorkDirForm(t.cfg.GetWorkDirs())
	if saved {
		if err := t.handler(CmdSetWorkDirs, dirs); err != nil {
			_ = zenity.Error("保存工作目录失败: "+err.Error(), zenity.Title("Sypora"))
		} else {
			_ = zenity.Info("工作目录配置已保存。", zenity.Title("Sypora"))
		}
	}
}

func (t *Tray) handleS3Config() {
	cfg, saved := showS3Form(t.cfg.GetS3Config())
	if saved {
		if err := t.handler(CmdSetS3Config, cfg); err != nil {
			_ = zenity.Error("保存 S3 配置失败: "+err.Error(), zenity.Title("Sypora"))
		} else {
			_ = zenity.Info("S3 配置已保存。", zenity.Title("Sypora"))
		}
	}
}

func (t *Tray) handleExportConfig() {
	filePath, err := zenity.SelectFileSave(
		zenity.Title("导出配置"),
		zenity.Filename("sypora-config.json"),
		zenity.ConfirmOverwrite(),
	)
	if err != nil || filePath == "" {
		return
	}
	if err := t.handler(CmdExportConfig, filePath); err != nil {
		_ = zenity.Error("导出失败: "+err.Error(), zenity.Title("Sypora"))
	} else {
		_ = zenity.Info("配置已成功导出。", zenity.Title("Sypora"))
	}
}

func (t *Tray) handleImportConfig() {
	filePath, err := zenity.SelectFile(
		zenity.Title("导入配置"),
		zenity.FileFilter{Name: "JSON 文件", Patterns: []string{"*.json"}},
	)
	if err != nil || filePath == "" {
		return
	}
	if err := t.handler(CmdImportConfig, filePath); err != nil {
		_ = zenity.Error("导入失败: "+err.Error(), zenity.Title("Sypora"))
	} else {
		_ = zenity.Info("配置已成功导入。", zenity.Title("Sypora"))
	}
}

func (t *Tray) handleVersion() {
	msg := "Sypora v" + Version + "\n\n" +
		"更新内容：\n" +
		"• 支持 S3 双向同步\n" +
		"• Windows 系统托盘集成\n" +
		"• 文件变更自动同步\n" +
		"• 配置导出/导入\n" +
		"• 反馈与更新功能"
	_ = zenity.Info(msg, zenity.Title("Sypora - 版本信息"))
}

func (t *Tray) handleFeedback() {
	text, submitted := showFeedbackForm()
	if !submitted {
		return
	}
	if err := t.handler(CmdSubmitFeedback, text); err != nil {
		_ = zenity.Error("提交反馈失败: "+err.Error(), zenity.Title("Sypora"))
	} else {
		_ = zenity.Info("感谢您的反馈！", zenity.Title("Sypora"))
	}
}

func (t *Tray) handleUpdate() {
	// Placeholder — will be implemented in a future iteration
}

func (t *Tray) updateModeChecks() {
	auto := t.cfg.GetSyncMode() == config.SyncModeAuto
	if auto {
		t.mAutoSync.Check()
		t.mManualSync.Uncheck()
	} else {
		t.mManualSync.Check()
		t.mAutoSync.Uncheck()
	}
}

func (t *Tray) updateAutoStartCheck() {
	if t.cfg.AutoStart {
		t.mAutoStart.Check()
	} else {
		t.mAutoStart.Uncheck()
	}
}

func normalizePrefix(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimLeft(p, "/")
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
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
