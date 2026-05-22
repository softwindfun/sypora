//go:build !windows

package tray

import "sypora/internal/config"

func showS3Form(cfg config.S3Config) (config.S3Config, bool) {
	return cfg, false
}
