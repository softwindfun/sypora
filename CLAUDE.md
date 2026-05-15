# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Sypora is a Windows system-tray application that syncs local working directories bidirectionally with S3-compatible object storage. Built in Go with low resource footprint (~10MB memory target).

## Build & Run

```bash
# Install dependencies
go mod tidy

# Development run (console mode, shows logs)
go run .

# Production build (no console window, stripped)
go build -ldflags "-H windowsgui -s -w" -o sypora.exe .
```

## Architecture

```
main.go → app.Run() → tray.Run() [blocks]
                ↓
    onTrayCommand dispatches to config/syncer/watcher
```

**Key flows:**
- `app.App` orchestrates the lifecycle: loads config → opens SQLite store → creates S3 client → creates syncer → creates watcher → starts tray (blocking)
- Config lives in `%APPDATA%/Sypora/config.json`, database in `%APPDATA%/Sypora/sypora.db`
- Tray commands callback to `app.onTrayCommand` which updates config/services
- `syncer.Syncer` runs a loop: immediate full scan on trigger, plus periodic full scan every 5 minutes
- `watcher.Watcher` uses fsnotify to detect local filesystem changes (debounced 500ms); currently events are logged but full sync is the reconciliation mechanism
- Conflict resolution: remote version is downloaded as `filename.sypora-conflict-<timestamp>.ext` alongside the local version

## Module responsibilities

| Module | Role |
|--------|------|
| `internal/app/` | Lifecycle, command dispatch, auto-start registry |
| `internal/tray/` | System tray UI (systray + zenity dialogs) |
| `internal/config/` | JSON config load/save, thread-safe accessors |
| `internal/syncer/` | Sync engine: `plan.go` diffs local↔remote, `syncer.go` executes actions, `conflict.go` names conflict copies |
| `internal/s3client/` | Thin wrapper over minio-go: upload, download, list, delete, stat |
| `internal/store/` | SQLite via modernc (pure Go, no CGO): file state CRUD, change detection via ETag/mtime/size comparison |
| `internal/watcher/` | fsnotify wrapper: recursive directory watching, debounce, ignore patterns |

## Sync plan logic

`buildPlan()` in `syncer/plan.go`:
1. Scans local file tree and remote object list
2. Joins against SQLite state table (last-known ETag, mtime, size)
3. For each file: detects new/removed/changed/conflict and emits SyncActions
4. Conflict = local changed AND remote changed since last sync

## Key dependencies

- `getlantern/systray` — Windows system tray
- `ncruces/zenity` — native Windows dialogs (folder picker, text entry, list)
- `minio/minio-go/v7` — S3-compatible client
- `fsnotify/fsnotify` — filesystem events
- `modernc.org/sqlite` — pure-Go SQLite (no CGO needed)
- `golang.org/x/sys/windows/registry` — Windows registry for auto-start
