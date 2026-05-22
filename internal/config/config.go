package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func ConfigDir() (string, error) {
	return configDir()
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

func (c *Config) ExportJSON() ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("cannot marshal config: %w", err)
	}
	return data, nil
}

func (c *Config) ImportFromJSON(data []byte) error {
	imported := struct {
		WorkDirs  []WorkDir `json:"work_dirs"`
		S3        S3Config  `json:"s3"`
		SyncMode  SyncMode  `json:"sync_mode"`
		AutoStart bool      `json:"auto_start"`
	}{}
	if err := json.Unmarshal(data, &imported); err != nil {
		return fmt.Errorf("cannot parse config: %w", err)
	}

	if imported.SyncMode == "" {
		imported.SyncMode = SyncModeAuto
	}
	if imported.SyncMode != SyncModeAuto && imported.SyncMode != SyncModeManual {
		return fmt.Errorf("invalid sync_mode: %q", imported.SyncMode)
	}

	// Trim and normalize
	imported.S3.Endpoint = strings.TrimSpace(imported.S3.Endpoint)
	imported.S3.AccessKey = strings.TrimSpace(imported.S3.AccessKey)
	imported.S3.SecretKey = strings.TrimSpace(imported.S3.SecretKey)
	imported.S3.Bucket = strings.TrimSpace(imported.S3.Bucket)
	imported.S3.Region = strings.TrimSpace(imported.S3.Region)

	for i := range imported.WorkDirs {
		imported.WorkDirs[i].LocalPath = strings.TrimSpace(imported.WorkDirs[i].LocalPath)
		imported.WorkDirs[i].RemotePath = strings.TrimLeft(imported.WorkDirs[i].RemotePath, "/")
		if !strings.HasSuffix(imported.WorkDirs[i].RemotePath, "/") {
			imported.WorkDirs[i].RemotePath += "/"
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.WorkDirs = imported.WorkDirs
	c.S3 = imported.S3
	c.SyncMode = imported.SyncMode
	c.AutoStart = imported.AutoStart
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
	return c.S3.Endpoint != "" &&
		c.S3.AccessKey != "" &&
		c.S3.SecretKey != "" &&
		c.S3.Bucket != "" &&
		c.S3.Region != ""
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
	s3.Endpoint = strings.TrimSpace(s3.Endpoint)
	s3.AccessKey = strings.TrimSpace(s3.AccessKey)
	s3.SecretKey = strings.TrimSpace(s3.SecretKey)
	s3.Bucket = strings.TrimSpace(s3.Bucket)
	s3.Region = strings.TrimSpace(s3.Region)
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
