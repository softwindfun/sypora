package app

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

func setAutoStart(enable bool) error {
	k, err := registry.OpenKey(
		registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`,
		registry.SET_VALUE|registry.QUERY_VALUE,
	)
	if err != nil {
		return fmt.Errorf("open registry: %w", err)
	}
	defer k.Close()

	if enable {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("get executable: %w", err)
		}
		exePath, err := filepath.Abs(exe)
		if err != nil {
			return fmt.Errorf("resolve exe path: %w", err)
		}
		return k.SetStringValue("Sypora", exePath)
	}
	return k.DeleteValue("Sypora")
}
