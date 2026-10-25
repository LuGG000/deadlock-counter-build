# Deadlock Counter-Build

Local tool (Go, standard library only) for Deadlock: pick your hero and the enemy team, and the tool adjusts your
default build with a few counter items and shows the purchase order. The build can be exported as an in-game import string.

## Install

Download `deadlock-counter-setup.exe` from the [latest release](https://github.com/LuGG000/deadlock-counter-build/releases/latest) and run it.
The installer downloads the newest `deadlock-counter.exe`, installs it per user (no admin rights needed) and adds a Start menu entry.
The app then keeps itself up to date. Neither Go nor any other runtime is required. The files are unsigned, so Windows SmartScreen may
show a warning: click "More info" and then "Run anyway".

## Run (from source)

```
go build -o deadlock-counter.exe .
./deadlock-counter.exe            # opens http://127.0.0.1:47871
```

Options: `-port`, `-nobrowser`, `-no-icons` (disables all network access), `-no-update`, `-assets-api <url>`.
Windows build from Linux: `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-s -w"`.

## Features

- Item scoring based on the enemy composition (counter rules, team profile, protection of the damage core), computed in ~100 µs.
- Purchase order (final build, max. 12 slots) and export as an in-game build (Base64 → Zstd → Protobuf `CMsgHeroBuild`),
  including skill order and "Optional" categories.
- Custom default builds per hero (`defaults.json`), icons, right-click/reset in the matchup.
- Live data (at startup, cached for 24 h): item values and IDs from the asset API, skill orders from deadlock-api match data,
  builds from tracklock.gg. If a request fails, the built-in snapshot is used.

## Data

`data/items.json` and `data/heroes.md` are embedded (as of 22 Oct 2026). If `deadlock_items_*.json` or
`deadlock_hero_catalog.md` are placed next to the exe, they take precedence. Heroes without a Tracklock build get a build
derived from similar heroes (marked in the tool).

## Settings (not in the repo)

`settings.json` next to the exe: `{"author_account_id": 123456789}` – the Steam32 ID is embedded in every own in-game export string.

## Updates and releases

At startup the exe checks the latest GitHub release. If it is newer than the running version, the exe downloads
`deadlock-counter.exe` (SHA-256 check), replaces itself and restarts. Failures are only logged. Development builds
(`version = "dev"`) and `-no-icons` / `-no-update` skip the check.

To create a release, push a tag `vX.Y.Z`. The workflow `.github/workflows/release.yml` runs the tests, builds the Windows exe
with the embedded version and creates the release.

## Tests

```
go test ./...
```
