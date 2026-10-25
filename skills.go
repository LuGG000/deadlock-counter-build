package main

// Skill-Reihenfolge je Held aus echten Matchdaten (deadlock-api: /v1/analytics/ability-order-stats).
// Das Tool laedt sie auf dem PC des Nutzers (Cache: skillorders.json neben der exe, 24h).
// Gewaehlt wird pro Held die Reihenfolge mit der besten Wilson-Untergrenze der Siegrate.

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type skillEntry struct {
	Seq     []uint32 `json:"seq"`
	Wins    uint64   `json:"wins"`
	Matches uint64   `json:"matches"`
	At      int64    `json:"at"`
}

type Skills struct {
	mu      sync.Mutex
	path    string
	api     string
	client  *http.Client
	enabled bool
	running bool
	status  string
	m       map[string]skillEntry // Schluessel = Heldenname (norm)
}

var skills *Skills

func newSkills(api string, enabled bool) *Skills {
	s := &Skills{path: filepath.Join(appDir(), "skillorders_v2.json"), api: api, enabled: enabled,
		client: &http.Client{Timeout: 25 * time.Second}, m: map[string]skillEntry{}}
	if b, err := os.ReadFile(s.path); err == nil {
		_ = json.Unmarshal(b, &s.m)
	}
	return s
}

func (s *Skills) stale() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.m) < len(heroAbil)/2 {
		return true
	}
	for _, e := range s.m {
		return time.Now().Unix()-e.At > 24*3600
	}
	return true
}

func (s *Skills) Status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func wilson(w, n uint64) float64 {
	if n == 0 {
		return 0
	}
	const z = 1.96
	p := float64(w) / float64(n)
	nn := float64(n)
	return (p + z*z/(2*nn) - z*math.Sqrt((p*(1-p)+z*z/(4*nn))/nn)) / (1 + z*z/nn)
}

// valid: Folge nutzt nur die 4 Signature-Faehigkeiten des Helden, jede hoechstens 4x.
func validSeq(key string, seq []uint32) bool {
	ha, ok := heroAbil[key]
	if !ok || len(seq) < 12 || len(seq) > 16 {
		return false
	}
	cnt := map[uint32]int{}
	for _, id := range seq {
		found := false
		for _, a := range ha.ID {
			if a == id {
				found = true
			}
		}
		if !found {
			return false
		}
		cnt[id]++
		if cnt[id] > 4 {
			return false
		}
	}
	return true
}

type abRow struct {
	Abilities []uint32 `json:"abilities"`
	Wins      uint64   `json:"wins"`
	Matches   uint64   `json:"matches"`
}

func (s *Skills) fetchHero(key string, heroID uint32) (skillEntry, error) {
	var rows []abRow
	try := func(q string) error {
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/v1/analytics/ability-order-stats?hero_id=%d&%s", s.api, heroID, q), nil)
		req.Header.Set("User-Agent", "DeadlockCounterBuild/1.0 (lokales Privatprojekt)")
		resp, err := s.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		rows = nil
		return json.Unmarshal(b, &rows)
	}
	err := try("min_ability_upgrades=16&min_matches=50")
	if err != nil || len(rows) == 0 {
		if e2 := try("min_matches=50"); e2 != nil {
			if err == nil {
				err = e2
			}
		} else {
			err = nil
		}
	}
	if err != nil {
		return skillEntry{}, err
	}
	// Nur gut belegte Reihenfolgen: mindestens 100 Matches UND mindestens 4 % aller beobachteten Matches
	// (verhindert z.B. 86 % Siegrate bei 38 Matches). Danach beste Wilson-Untergrenze.
	var total uint64
	for _, r := range rows {
		if validSeq(key, r.Abilities) {
			total += r.Matches
		}
	}
	minM := uint64(float64(total) * 0.04)
	if minM < 100 {
		minM = 100
	}
	var elig []abRow
	for _, r := range rows {
		if r.Matches >= minM && validSeq(key, r.Abilities) {
			elig = append(elig, r)
		}
	}
	if len(elig) == 0 { // wenig Daten: die meistgespielte gueltige Reihenfolge
		for _, r := range rows {
			if r.Matches >= 30 && validSeq(key, r.Abilities) {
				elig = append(elig, r)
			}
		}
		sort.Slice(elig, func(i, j int) bool { return elig[i].Matches > elig[j].Matches })
		if len(elig) > 1 {
			elig = elig[:1]
		}
	}
	sort.Slice(elig, func(i, j int) bool {
		return wilson(elig[i].Wins, elig[i].Matches) > wilson(elig[j].Wins, elig[j].Matches)
	})
	if len(elig) > 0 {
		r := elig[0]
		return skillEntry{Seq: r.Abilities, Wins: r.Wins, Matches: r.Matches, At: time.Now().Unix()}, nil
	}
	return skillEntry{}, fmt.Errorf("keine passende Reihenfolge (%d Zeilen)", len(rows))
}

func (s *Skills) start(force bool) bool {
	if !s.enabled {
		return false
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return false
	}
	s.running = true
	s.mu.Unlock()
	go func() {
		ok, fail := 0, 0
		var lastErr error
		for key := range heroAbil {
			hid, has := heroIDs[key]
			if !has {
				continue
			}
			e, err := s.fetchHero(key, hid)
			s.mu.Lock()
			if err != nil {
				fail++
				lastErr = err
			} else {
				ok++
				s.m[key] = e
			}
			s.status = fmt.Sprintf("Skill-Daten: %d geladen, %d fehlgeschlagen", ok, fail)
			s.mu.Unlock()
			time.Sleep(120 * time.Millisecond)
		}
		s.mu.Lock()
		if ok > 0 {
			if b, err := json.MarshalIndent(s.m, "", " "); err == nil {
				_ = os.WriteFile(s.path, b, 0644)
			}
		}
		if lastErr != nil && ok == 0 {
			s.status = "Skill-Daten nicht ladbar: " + lastErr.Error()
		}
		s.running = false
		s.mu.Unlock()
	}()
	return true
}

// info: Reihenfolge fuer den Anzeige-/Export-Zweck.
type SkillInfo struct {
	Source  string   `json:"source"` // "live" | "vorlage"
	Matches uint64   `json:"matches"`
	WR      float64  `json:"wr"`
	Steps   []string `json:"steps"` // Faehigkeitsname je Punkt
}

func (s *Skills) seqFor(key string) ([]uint32, SkillInfo) {
	ha, ok := heroAbil[key]
	if !ok {
		return nil, SkillInfo{Source: "keine"}
	}
	if s != nil {
		s.mu.Lock()
		e, has := s.m[key]
		s.mu.Unlock()
		if has && validSeq(key, e.Seq) {
			info := SkillInfo{Source: "live", Matches: e.Matches, WR: math.Round(1000*float64(e.Wins)/float64(e.Matches)) / 10}
			for _, id := range e.Seq {
				for i, a := range ha.ID {
					if a == id {
						info.Steps = append(info.Steps, ha.Names[i])
					}
				}
			}
			return e.Seq, info
		}
	}
	// Vorlage (nur Notnagel, nicht aus Matchdaten)
	p := func(i int) uint32 { return ha.ID[ha.Order[i]-1] }
	seq := []uint32{p(0), p(0), p(1), p(2), p(0), p(1), p(3), p(1), p(1), p(2), p(2), p(2), p(0), p(3), p(3), p(3)}
	info := SkillInfo{Source: "vorlage"}
	for _, id := range seq {
		for i, a := range ha.ID {
			if a == id {
				info.Steps = append(info.Steps, ha.Names[i])
			}
		}
	}
	return seq, info
}

// skillOrder: Protobuf-Bytes (AbilityOrder). Auftreten 1 = freischalten (type 2), 2/3/4 = Upgrade T1/T2/T3 (type 1).
func skillOrder(heroKey string) ([]byte, SkillInfo) {
	seq, info := skills.seqFor(heroKey)
	if seq == nil {
		return nil, info
	}
	cnt := map[uint32]int{}
	var o []byte
	for _, id := range seq {
		cnt[id]++
		t, delta := 1, 0
		switch cnt[id] {
		case 1:
			t, delta = 2, -1
		case 2:
			delta = -1
		case 3:
			delta = -2
		default:
			delta = -5
		}
		var c []byte
		c = pbU(c, 1, uint64(id))
		c = pbU(c, 2, uint64(t))
		c = pbU(c, 3, uint64(int64(delta)))
		o = pbBytes(o, 1, c)
	}
	return o, info
}
