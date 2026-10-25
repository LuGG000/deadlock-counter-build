# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Local Go tool (standard library only, Go 1.24) for Deadlock. The user picks their hero and the enemy team; the tool adjusts the hero's default build with a few counter-item swaps and exports it as an in-game import string. `main` starts an HTTP server on 127.0.0.1 (default port 47871) and opens the embedded `ui.html` in the browser.

The repo root is the Go source folder. The exe and runtime files are expected next to each other outside the repo (see the build command below).

## Commands

```
go build -ldflags "-s -w" -o ../deadlock-counter.exe .   # build into the parent folder
go run . -nobrowser -no-icons                             # run without browser and without any network access
go test ./...
go test -run TestParseTracklock ./...                     # single test
```

Flags: `-port`, `-nobrowser`, `-no-icons` (disables all network access, including live sync and the updater), `-no-update`, `-assets-api <url>`.

## Architecture

- **Data loading (`main.go: loadData`)**: item data comes from `data/items.json`, hero catalog and default builds from `data/heroes.md` (a Markdown catalog parsed by `parseHeroes`). Both are embedded via `go:embed`. Files named `deadlock_items_*.json` / `deadlock_hero_catalog.md` next to the exe override them, so a patch update needs no rebuild.
- **Data enrichment at startup (order matters)**: `applyTracklock` (`tracklock.go`) scrapes default builds from tracklock.gg and replaces catalog builds; `deriveMissing` (`derive.go`) builds defaults for heroes without Tracklock data from similar heroes; `syncLive` (`live.go`) then updates item stats and hero/ability IDs from the asset API. All of it is cached next to the exe (`live_cache.json`, `tracklock_cache.json`, 24 h) and falls back to the embedded state when offline.
- **Scoring (`main.go`)**: `computeVotes` collects points per item from counter lists, the team profile (`makeProfile`) and rule tables; `recommend` turns the votes into swaps, protects the hero's damage core and enforces slot limits (max. 12 slots). The result is `CalcResp`, served by `/api/calc`.
- **Export (`export.go`)**: builds the in-game share string: Base64 → Zstandard (raw/hand-rolled with `xxh64`, no zstd dependency) → Protobuf `CMsgHeroBuild`. Item IDs are MurmurHash2 (seed `0x31415926`) of the item's `internal_name`. New heroes need an entry in `heroIDs`. `settings.json` supplies `author_account_id`.
- **Skills (`skills.go`, `abil_gen.go`)**: `heroAbil` is generated static data (ability IDs and default skill order per hero). `skills.go` fetches real skill orders from match data (Wilson score) into `skillorders*.json`.
- **Icons (`icons.go`)**: downloads hero/item icons once into `icons/` next to the exe; local files with normalized names (`norm`) are also picked up.
- **Self-update (`update.go`)**: `selfUpdate` runs first in `main()`; if the latest GitHub release tag is newer than `version` (set via `-ldflags "-X main.version=vX.Y.Z"`, `dev` never updates) it downloads the `deadlock-counter.exe` asset, verifies the digest, swaps the exe (old one renamed to `.old`) and restarts. Releases are built by `.github/workflows/release.yml` on `v*` tags.
- **Installer (`installer/installer.iss`)**: Inno Setup script built by the `installer` job in the release workflow. It downloads the latest `deadlock-counter.exe` at install time and installs per user into `%LOCALAPPDATA%ProgramsDeadlockCounter` (must stay user-writable, the updater and runtime files need it).
- **Frontend**: single file `ui.html`, embedded; talks to `/api/*` endpoints registered in `main()`.
- **Runtime files** (`defaults.json`, `settings.json`, `icons/`, `*_cache.json`, `skillorders*.json`) live next to the exe and are git-ignored.

## Conventions

- Hero/item names are matched through `norm()` / `nk()` normalization (lowercase, no special characters); use these instead of comparing raw names.
