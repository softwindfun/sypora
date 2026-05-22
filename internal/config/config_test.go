package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSave(t *testing.T) {
	// Override config dir for testing
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.SyncMode != SyncModeAuto {
		t.Fatalf("default sync mode should be auto, got: %s", c.SyncMode)
	}

	c.SetSyncMode(SyncModeManual)
	c.SetWorkDirs([]WorkDir{
		{LocalPath: `C:\notes`, RemotePath: "notes/"},
	})
	c.SetS3Config(S3Config{
		Endpoint:  "s3.example.com",
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "mybucket",
		Region:    "us-east-1",
		UseSSL:    true,
	})

	if err := c.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Reload
	c2, err := Load()
	if err != nil {
		t.Fatalf("Load2: %v", err)
	}

	if c2.GetSyncMode() != SyncModeManual {
		t.Fatalf("sync mode not persisted")
	}
	dirs := c2.GetWorkDirs()
	if len(dirs) != 1 || dirs[0].LocalPath != `C:\notes` {
		t.Fatalf("work dirs not persisted: %v", dirs)
	}
	s3 := c2.GetS3Config()
	if s3.Bucket != "mybucket" {
		t.Fatalf("s3 config not persisted: %v", s3)
	}
}

func TestHasWorkDirs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	c, _ := Load()
	if c.HasWorkDirs() {
		t.Fatal("new config should have no work dirs")
	}

	c.SetWorkDirs([]WorkDir{{LocalPath: "/tmp", RemotePath: "tmp/"}})
	if !c.HasWorkDirs() {
		t.Fatal("should have work dirs after setting")
	}
}

func TestHasS3Config(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	c, _ := Load()
	if c.HasS3Config() {
		t.Fatal("new config should have no s3 config")
	}

	c.SetS3Config(S3Config{
		Endpoint: "s3.example.com", AccessKey: "ak",
		SecretKey: "sk", Bucket: "b", Region: "us-east-1",
	})
	if !c.HasS3Config() {
		t.Fatal("should have s3 config after setting")
	}
}

func TestConfigDirFallback(t *testing.T) {
	// Unset APPDATA, should fall back to UserHomeDir
	t.Setenv("APPDATA", "")

	dir, err := configDir()
	if err != nil {
		t.Fatalf("configDir: %v", err)
	}

	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, "AppData", "Roaming", "Sypora")
	if dir != expected {
		t.Fatalf("expected %s, got %s", expected, dir)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	c, _ := Load()
	c.SetSyncMode(SyncModeManual)
	c.SetWorkDirs([]WorkDir{
		{LocalPath: `C:\notes`, RemotePath: "notes/"},
		{LocalPath: `D:\docs`, RemotePath: "/docs/"},
	})
	c.SetS3Config(S3Config{
		Endpoint:  "s3.example.com",
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "mybucket",
		Region:    "us-east-1",
		UseSSL:    true,
	})
	c.SetAutoStart(true)

	data, err := c.ExportJSON()
	if err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}

	c2, _ := Load()
	if err := c2.ImportFromJSON(data); err != nil {
		t.Fatalf("ImportFromJSON: %v", err)
	}

	if c2.GetSyncMode() != SyncModeManual {
		t.Fatalf("sync mode: got %s, want manual", c2.GetSyncMode())
	}
	dirs := c2.GetWorkDirs()
	if len(dirs) != 2 {
		t.Fatalf("work dirs count: got %d, want 2", len(dirs))
	}
	if dirs[0].LocalPath != `C:\notes` || dirs[0].RemotePath != "notes/" {
		t.Fatalf("work dir[0]: %+v", dirs[0])
	}
	if dirs[1].LocalPath != `D:\docs` || dirs[1].RemotePath != "docs/" {
		t.Fatalf("work dir[1] (remote should be normalized): %+v", dirs[1])
	}
	s3 := c2.GetS3Config()
	if s3.Bucket != "mybucket" {
		t.Fatalf("s3 config: %+v", s3)
	}
	if !c2.AutoStart {
		t.Fatal("auto_start should be true")
	}
}

func TestImportFromJSONDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	c, _ := Load()
	// Empty sync_mode should default to auto
	err := c.ImportFromJSON([]byte(`{"sync_mode":""}`))
	if err != nil {
		t.Fatalf("ImportFromJSON: %v", err)
	}
	if c.GetSyncMode() != SyncModeAuto {
		t.Fatalf("empty sync_mode should default to auto, got %s", c.GetSyncMode())
	}
}

func TestImportFromJSONInvalidMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	c, _ := Load()
	err := c.ImportFromJSON([]byte(`{"sync_mode":"bogus"}`))
	if err == nil {
		t.Fatal("expected error for invalid sync_mode")
	}
}

func TestImportFromJSONTrimsWhitespace(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	c, _ := Load()
	err := c.ImportFromJSON([]byte(`{
		"s3": {
			"endpoint": "  s3.example.com  ",
			"access_key": "  ak  ",
			"secret_key": "  sk  ",
			"bucket": "  mybucket  ",
			"region": "  us-east-1  "
		}
	}`))
	if err != nil {
		t.Fatalf("ImportFromJSON: %v", err)
	}
	s3 := c.GetS3Config()
	if s3.Endpoint != "s3.example.com" {
		t.Fatalf("endpoint not trimmed: %q", s3.Endpoint)
	}
	if s3.Bucket != "mybucket" {
		t.Fatalf("bucket not trimmed: %q", s3.Bucket)
	}
}
