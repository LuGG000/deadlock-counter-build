# Deadlock Counter-Build

Lokales Tool (Go, nur Standardbibliothek) für Deadlock: Du wählst deinen Helden und das Gegnerteam, das Tool passt dein
Standard-Build mit wenigen Counter-Items an und zeigt die Kaufreihenfolge. Das Build lässt sich als Ingame-Import-String exportieren.

## Starten

```
go build -o deadlock-counter.exe .
./deadlock-counter.exe            # öffnet http://127.0.0.1:47871
```

Optionen: `-port`, `-nobrowser`, `-no-icons` (schaltet jeden Netzzugriff ab), `-assets-api <url>`.
Windows-Build aus Linux: `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-s -w"`.

## Funktionen

- Bewertung der Items nach Gegnerzusammensetzung (Counter-Regeln, Teamprofil, Schutz des Schadens-Kerns), Berechnung in ~100 µs.
- Kaufreihenfolge (Endbuild, max. 12 Slots) und Export als Ingame-Build (Base64 → Zstd → Protobuf `CMsgHeroBuild`),
  inklusive Skill-Reihenfolge und „Optional“-Kategorien.
- Eigene Default-Builds pro Held (`defaults.json`), Icons, Rechtsklick/Reset im Matchup.
- Live-Daten (beim Start, Cache 24 h): Item-Werte und IDs aus der Asset-API, Skill-Reihenfolgen aus Matchdaten der deadlock-api,
  Builds von tracklock.gg. Schlägt ein Abruf fehl, gilt der eingebaute Stand.

## Daten

`data/items.json` und `data/heroes.md` sind eingebettet (Stand 22.10.2026). Liegen `deadlock_items_*.json` oder
`deadlock_hero_catalog.md` neben der exe, haben diese Vorrang. Helden ohne Tracklock-Build bekommen ein aus ähnlichen
Helden abgeleitetes Build (im Tool markiert).

## Einstellungen (nicht im Repo)

`settings.json` neben der exe: `{"author_account_id": 123456789}` – die Steam32-ID steht in jedem eigenen Ingame-Export-String.

## Tests

```
go test ./...
```
