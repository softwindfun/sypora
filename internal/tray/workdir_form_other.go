//go:build !windows

package tray

import "sypora/internal/config"

func showWorkDirForm(dirs []config.WorkDir) ([]config.WorkDir, bool) {
	return dirs, false
}
