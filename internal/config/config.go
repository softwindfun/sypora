package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type SyncMode string

const (
	SyncModeAuto   SyncMode = "auto"
	SyncModeManual SyncMode = "manual"
)

type WorkDir struct {
	LocalPath  string `json:"local_path"`
	RemotePath string `json:"remote_path"` // prefix in S3 bucket
}

type S3Config struct {
	Endpoint  string `json:"endpoint"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	Bucket    string `json:"bucket"`
	Region    string `json:"region"`
	UseSSL    bool   `json:"use_ssl"`
}

type Config struct {
	WorkDirs  []WorkDir `json:"work_dirs"`
	S3        S3Config  `json:"s3"`
	SyncMode  SyncMode  `json:"sync_mode"`
	AutoStart bool      `json:"auto_start"`

	mu       sync.RWMutex
	filePath string
}

func configDir() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine config directory: %w", err)
		}
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	dir := filepath.Join(appData, "Sypora")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("cannot create config directory: %w", err)
	}
	return dir, nil
}

func Load() (*Config, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "config.json")

	c := &Config{
		SyncMode: SyncModeAuto,
		filePath: path,
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read config: %w", err)
	}

	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("cannot parse config: %w", err)
	}

	// defaults
	if c.SyncMode == "" {
		c.SyncMode = SyncModeAuto
	}

	return c, nil
}

func (c *Config) Save() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot marshal config: %w", err)
	}
	if err := os.WriteFile(c.filePath, data, 0600); err != nil {
		return fmt.Errorf("cannot write config: %w", err)
	}
	return nil
}

func (c *Config) HasWorkDirs() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.WorkDirs) > 0
}

func (c *Config) HasS3Config() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.S3.Endpoint != "" && c.S3.Bucket != ""
}

func (c *Config) GetWorkDirs() []WorkDir {
	c.mu.RLock()
	defer c.mu.RUnlock()
	dirs := make([]WorkDir, len(c.WorkDirs))
	copy(dirs, c.WorkDirs)
	return dirs
}

func (c *Config) GetS3Config() S3Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.S3
}

func (c *Config) GetSyncMode() SyncMode {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.SyncMode
}

func (c *Config) SetWorkDirs(dirs []WorkDir) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.WorkDirs = dirs
}

func (c *Config) SetS3Config(s3 S3Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.S3 = s3
}

func (c *Config) SetSyncMode(mode SyncMode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.SyncMode = mode
}

func (c *Config) SetAutoStart(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.AutoStart = v
}
