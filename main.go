// Deadlock Counter-Build: fast local server + lightweight browser UI.
// Standard library only. Data is parsed once at startup; each calculation takes microseconds.
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed data/items.json data/heroes.md ui.html assets/icon.ico
var assets embed.FS

const (
	maxActives    = 4
	maxSlots      = 12 // 9 + 3 extra slots
	protectedCore = 8  // only swap the first core entries for a very strong reason
	spike         = 4800
	minGain       = 1.5
)

// ------------------------------------------------------------------ Helpers

func norm(s string) string {
	s = strings.ReplaceAll(s, "\u2019", "'")
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hasKey(list []string, key string) bool {
	for _, x := range list {
		if norm(x) == key {
			return true
		}
	}
	return false
}

var catIdx = map[string]int{"Weapon": 0, "Vitality": 1, "Spirit": 2}

// ------------------------------------------------------------------ Data model

type Item struct {
	Name   string `json:"name"`
	Cat    string `json:"cat"`
	Tier   int    `json:"tier"`
	Cost   int    `json:"cost"`
	Active bool   `json:"active"`
	Tip    string `json:"tip"`
	IName  string `json:"-"` // internal name (for the export string)
	key    string
	comps  []string
	grp    int // function group or -1
	ci     int // category index or -1
}

type Build struct {
	Early []string `json:"early"`
	Core  []string `json:"core"`
	Late  []string `json:"late"`
}

type Hero struct {
	Name                                      string  `json:"name"`
	Role                                      string  `json:"role"`
	Shares                                    [3]int  `json:"shares"` // W/V/S in %
	Flags                                     string  `json:"flags"`
	HasBuild                                  bool    `json:"hasBuild"`
	Derived                                   bool    `json:"derived"`        // build derived from similar heroes
	DerivedCounter                            bool    `json:"derivedCounter"` // counter items derived
	WR                                        float64 `json:"wr"`
	Catalog                                   Build   `json:"catalog"`
	head                                      string
	counter                                   []string
	cKeys                                     []string
	mix                                       [3]int
	hasMix                                    bool
	isCC, isSilence, isHeal, isSlow, isMobile bool
}

type Data struct {
	Meta    map[string]any
	Items   []*Item
	ItemMap map[string]*Item
	Heroes  []*Hero
	HeroMap map[string]*Hero
	Source  string
}

type itemJSON struct {
	Name         string `json:"name"`
	InternalName string `json:"internal_name"`
	Category     string `json:"category"`
	Tier         any    `json:"tier"`
	Cost         int    `json:"cost_souls"`
	SB           bool   `json:"street_brawl_only"`
	Activation   string `json:"activation"`
	Components   []struct {
		Name string `json:"name"`
	} `json:"components"`
	Stats []struct {
		Label string      `json:"label"`
		Value interface{} `json:"value"`
		Unit  string      `json:"unit"`
	} `json:"stats"`
	Effects []struct {
		Description string `json:"description"`
	} `json:"effects"`
}

type itemsFile struct {
	Meta struct {
		AsOf      string `json:"as_of_date"`
		GameBuild struct {
			ClientVersion int `json:"client_version"`
		} `json:"game_build"`
	} `json:"meta"`
	Tiers map[string]struct {
		Items map[string][]itemJSON `json:"items"`
	} `json:"tiers"`
}

func fmtVal(v interface{}) string {
	switch t := v.(type) {
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprint(t)
	}
}

func parseItems(raw []byte) ([]*Item, map[string]*Item, map[string]any, error) {
	var f itemsFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, nil, nil, err
	}
	var items []*Item
	m := map[string]*Item{}
	for _, t := range f.Tiers {
		for _, lst := range t.Items {
			for _, ij := range lst {
				if ij.SB {
					continue
				}
				tier := 0
				if tf, ok := ij.Tier.(float64); ok {
					tier = int(tf)
				}
				it := &Item{Name: ij.Name, IName: ij.InternalName, Cat: ij.Category, Tier: tier, Cost: ij.Cost,
					Active: ij.Activation == "active", key: norm(ij.Name), grp: -1, ci: -1}
				if ci, ok := catIdx[ij.Category]; ok {
					it.ci = ci
				}
				if g, ok := itemGroup[it.key]; ok {
					it.grp = g
				}
				for _, c := range ij.Components {
					it.comps = append(it.comps, norm(c.Name))
				}
				var parts []string
				for _, s := range ij.Stats {
					p := s.Label + " " + fmtVal(s.Value) + s.Unit
					parts = append(parts, p)
				}
				tip := strings.Join(parts, ", ")
				if len(ij.Effects) > 0 {
					d := strings.ReplaceAll(ij.Effects[0].Description, "\n", " ")
					if len(d) > 240 {
						d = d[:240] + "…"
					}
					if tip != "" {
						tip += " | "
					}
					tip += d
				}
				it.Tip = tip
				if _, dup := m[it.key]; dup {
					continue
				}
				m[it.key] = it
				items = append(items, it)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Cat != items[j].Cat {
			return items[i].Cat < items[j].Cat
		}
		if items[i].Tier != items[j].Tier {
			return items[i].Tier < items[j].Tier
		}
		return items[i].Name < items[j].Name
	})
	meta := map[string]any{"asOf": f.Meta.AsOf, "build": f.Meta.GameBuild.ClientVersion}
	return items, m, meta, nil
}

var (
	splitRe   = regexp.MustCompile(`\s>\s|,\s*`)
	mixRe     = regexp.MustCompile(`Core-Mix W/V/S:\s*(\d+)/(\d+)/(\d+)`)
	wrRe      = regexp.MustCompile(`WR\s*([\d.]+)%`)
	counterRe = regexp.MustCompile(`^Counter \(items against .+?\):\s*(.*)$`)
	mobileRe  = regexp.MustCompile(`(?i)mobility|nimble|high-flying|acrobatics|traveller|slippery`)
	blockRe   = regexp.MustCompile(`\n(?:### )`)
)

func splitItems(s string) []string {
	var out []string
	for _, x := range splitRe.Split(s, -1) {
		x = strings.TrimRight(strings.TrimSpace(x), ".")
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

func parseHeroes(text string) []*Hero {
	var heroes []*Hero
	idx := blockRe.FindAllStringIndex(text, -1)
	for i, loc := range idx {
		end := len(text)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		block := text[loc[0]+1 : end]
		lines := strings.Split(block, "\n")
		head := strings.TrimSpace(strings.TrimPrefix(lines[0], "### "))
		parts := strings.Split(head, " \u2014 ")
		h := &Hero{Name: strings.TrimSpace(parts[0]), head: head, Role: "?"}
		if len(parts) > 1 {
			h.Role = strings.TrimSpace(strings.Split(parts[1], ",")[0])
		}
		var abilities string
		for _, ln := range lines[1:] {
			ln = strings.TrimSpace(ln)
			switch {
			case strings.HasPrefix(ln, "Flags:"):
				h.Flags = strings.TrimSpace(ln[6:])
			case strings.HasPrefix(ln, "Abilities:"):
				abilities = ln
			case strings.HasPrefix(ln, "Default build"):
				if strings.Contains(ln, "NO Tracklock") {
					h.HasBuild = false
				} else {
					h.HasBuild = true
					if m := mixRe.FindStringSubmatch(ln); m != nil {
						for k := 0; k < 3; k++ {
							h.mix[k], _ = strconv.Atoi(m[k+1])
						}
						h.hasMix = true
					}
					if m := wrRe.FindStringSubmatch(ln); m != nil {
						h.WR, _ = strconv.ParseFloat(m[1], 64)
					}
				}
			case strings.HasPrefix(ln, "CORE:"):
				h.Catalog.Core = splitItems(ln[5:])
			case strings.HasPrefix(ln, "Early:"):
				body := ln[6:]
				if j := strings.Index(body, "| Late:"); j >= 0 {
					h.Catalog.Early = splitItems(body[:j])
					h.Catalog.Late = splitItems(body[j+7:])
				} else {
					h.Catalog.Early = splitItems(body)
				}
			default:
				if m := counterRe.FindStringSubmatch(ln); m != nil {
					h.counter = splitItems(m[1])
				}
			}
		}
		// Damage shares
		if h.hasMix {
			h.Shares = h.mix
		} else {
			ds, dg := strings.Count(abilities, "dmg:S"), strings.Count(abilities, "dmg:G")
			t := ds + dg
			if t == 0 {
				h.Shares = [3]int{40, 30, 30}
			} else {
				s := int(20 + 50*float64(ds)/float64(t) + 0.5)
				w := int(20 + 50*float64(dg)/float64(t) + 0.5)
				h.Shares = [3]int{w, 100 - s - w, s}
			}
		}
		fl := strings.ToLower(h.Flags)
		anyOf := func(ws ...string) bool {
			for _, w := range ws {
				if strings.Contains(fl, w) {
					return true
				}
			}
			return false
		}
		h.isCC = anyOf("stun", "sleep", "immobilize", "root")
		h.isSilence = anyOf("silence", "disarm")
		h.isHeal = anyOf("heal", "lifesteal")
		h.isSlow = anyOf("slow")
		h.isMobile = mobileRe.MatchString(h.head)
		for _, c := range h.counter {
			h.cKeys = append(h.cKeys, norm(c))
		}
		if h.Catalog.Early == nil {
			h.Catalog.Early = []string{}
		}
		if h.Catalog.Core == nil {
			h.Catalog.Core = []string{}
		}
		if h.Catalog.Late == nil {
			h.Catalog.Late = []string{}
		}
		heroes = append(heroes, h)
	}
	sort.Slice(heroes, func(i, j int) bool { return heroes[i].Name < heroes[j].Name })
	return heroes
}

// ------------------------------------------------------------------ Loading (external or embedded)

func findExternal(names ...string) (string, bool) {
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	if wd, err := os.Getwd(); err == nil {
		dirs = append(dirs, wd)
	}
	for _, d := range dirs {
		for _, n := range names {
			if strings.Contains(n, "*") {
				ms, _ := filepath.Glob(filepath.Join(d, n))
				if len(ms) > 0 {
					sort.Strings(ms)
					return ms[len(ms)-1], true
				}
			} else if _, err := os.Stat(filepath.Join(d, n)); err == nil {
				return filepath.Join(d, n), true
			}
		}
	}
	return "", false
}

func loadData() (*Data, error) {
	var itemsRaw, heroRaw []byte
	src := "embedded data"
	if p, ok := findExternal("deadlock_items_*.json"); ok {
		if b, err := os.ReadFile(p); err == nil {
			itemsRaw = b
			src = "files next to the exe"
		}
	}
	if p, ok := findExternal("deadlock_hero_catalog.md"); ok {
		if b, err := os.ReadFile(p); err == nil {
			heroRaw = b
		}
	}
	if itemsRaw == nil {
		itemsRaw, _ = assets.ReadFile("data/items.json")
	}
	if heroRaw == nil {
		heroRaw, _ = assets.ReadFile("data/heroes.md")
		src = "embedded data"
	}
	items, im, meta, err := parseItems(itemsRaw)
	if err != nil {
		return nil, err
	}
	heroes := parseHeroes(string(heroRaw))
	hm := map[string]*Hero{}
	for _, h := range heroes {
		hm[norm(h.Name)] = h
	}
	d := &Data{Meta: meta, Items: items, ItemMap: im, Heroes: heroes, HeroMap: hm, Source: src}
	applyTracklock(d)
	for _, line := range deriveMissing(d) {
		log.Printf("Derived (no Tracklock data): %s", line)
	}
	return d, nil
}

// ------------------------------------------------------------------ Scoring

type group struct {
	name  string
	names []string
	max   int
}

var groupDefs = []group{
	{"Purge/Unstoppable", []string{"Dispel Magic", "Unstoppable", "Divine Barrier", "Cloak of Opportunity", "Unstable Concoction"}, 1},
	{"Healing reduction", []string{"Healbane", "Toxic Bullets", "Inhibitor"}, 1},
	{"Bullet-Defense", []string{"Bullet Resilience", "Plated Armor", "Metal Skin", "Weapon Shielding", "Escalating Resilience", "Juggernaut", "Return Fire"}, 2},
	{"Spirit-Defense", []string{"Spirit Resilience", "Witchmail", "Spellbreaker", "Counterspell", "Spirit Shielding", "Diviner's Kevlar"}, 2},
	{"Silence/Lockdown", []string{"Cursed Relic", "Knockdown", "Silence Wave"}, 1},
}

var itemGroup = map[string]int{}

func init() {
	for gi, g := range groupDefs {
		for _, n := range g.names {
			itemGroup[norm(n)] = gi
		}
	}
}

type Votes struct {
	score   map[string]float64
	enemies map[string][]string
	rules   map[string][]string
}

func newVotes() *Votes {
	return &Votes{map[string]float64{}, map[string][]string{}, map[string][]string{}}
}

var normCache sync.Map

// nk: norm() with cache, only for fixed names (rules, hero data)
func nk(s string) string {
	if v, ok := normCache.Load(s); ok {
		return v.(string)
	}
	v := norm(s)
	normCache.Store(s, v)
	return v
}

func (v *Votes) counterKey(k string, pts float64, who string) {
	v.score[k] += pts
	v.enemies[k] = append(v.enemies[k], who)
}
func (v *Votes) counter(item string, pts float64, who string) {
	k := nk(item)
	v.score[k] += pts
	v.enemies[k] = append(v.enemies[k], who)
}
func (v *Votes) rule(item string, pts float64, text string) {
	k := nk(item)
	v.score[k] += pts
	for _, t := range v.rules[k] {
		if t == text {
			return
		}
	}
	v.rules[k] = append(v.rules[k], text)
}
func (v *Votes) penalty(item string, pts float64) { v.score[nk(item)] -= pts }
func (v *Votes) reason(k string) string {
	var parts []string
	if len(v.enemies[k]) > 0 {
		parts = append(parts, "Counter pick against "+strings.Join(v.enemies[k], ", "))
	}
	parts = append(parts, v.rules[k]...)
	if len(parts) == 0 {
		return "generally useful"
	}
	return strings.Join(parts, "; ")
}

type Profile struct {
	Gun, Spirit, CC, Silence, Heal, Slow, Mobile, Tank []string
}

func makeProfile(enemies []*Hero) *Profile {
	p := &Profile{}
	for _, e := range enemies {
		w, v, s := e.Shares[0], e.Shares[1], e.Shares[2]
		if w >= 50 {
			p.Gun = append(p.Gun, e.Name)
		}
		if s >= 50 {
			p.Spirit = append(p.Spirit, e.Name)
		}
		if v >= 35 {
			p.Tank = append(p.Tank, e.Name)
		}
		if e.isCC {
			p.CC = append(p.CC, e.Name)
		}
		if e.isSilence {
			p.Silence = append(p.Silence, e.Name)
		}
		if e.isHeal {
			p.Heal = append(p.Heal, e.Name)
		}
		if e.isSlow {
			p.Slow = append(p.Slow, e.Name)
		}
		if e.isMobile {
			p.Mobile = append(p.Mobile, e.Name)
		}
	}
	return p
}

func computeVotes(me *Hero, enemies []*Hero, lane *Hero) (*Votes, *Profile) {
	V := newVotes()
	prof := makeProfile(enemies)
	mw, ms := me.Shares[0], me.Shares[2]
	gunHero := mw >= ms

	for _, e := range enemies {
		w, s := e.Shares[0], e.Shares[2]
		isLane := lane != nil && e == lane
		carry := w >= 60 || s >= 60 || e.Role == "Assassin"
		weight := 1.0
		if isLane {
			weight++
		}
		if carry {
			weight++
		}
		if weight > 3 {
			weight = 3
		}
		tag := e.Name
		if isLane {
			tag += " (Lane)"
		}
		for _, k := range e.cKeys {
			V.counterKey(k, weight, tag)
		}
	}

	nh, ncc, ngun, nsp := len(prof.Heal), len(prof.CC), len(prof.Gun), len(prof.Spirit)
	if nh >= 2 {
		txt := fmt.Sprintf("%d healer/lifesteal heroes on the enemy team", nh)
		if gunHero {
			V.rule("Toxic Bullets", 3, txt)
			V.rule("Inhibitor", 2, txt)
		} else {
			V.rule("Healbane", 3, txt)
		}
	} else if nh == 1 {
		if gunHero {
			V.rule("Toxic Bullets", 1, "1 healer on the enemy team")
		} else {
			V.rule("Healbane", 1, "1 healer on the enemy team")
		}
	}
	if ncc >= 3 {
		txt := fmt.Sprintf("%d CC heroes (stun/sleep/root)", ncc)
		V.rule("Debuff Reducer", 3, txt)
		V.rule("Unstoppable", 2, txt)
	} else if ncc == 2 {
		V.rule("Debuff Reducer", 1, "2 CC heroes")
	}
	if len(prof.Silence) >= 2 {
		V.rule("Counterspell", 1, "several silence/disarm heroes")
	}
	if ngun >= 3 {
		txt := fmt.Sprintf("%d gun carries (%s)", ngun, strings.Join(prof.Gun, ", "))
		V.rule("Bullet Resilience", 3, txt)
		V.rule("Plated Armor", 2, txt)
		V.rule("Metal Skin", 1.5, txt)
		if nsp <= 1 {
			for _, n := range groupDefs[3].names {
				V.penalty(n, 3)
			}
		}
	}
	if nsp >= 3 {
		txt := fmt.Sprintf("%d spirit burst heroes (%s)", nsp, strings.Join(prof.Spirit, ", "))
		V.rule("Spirit Resilience", 3, txt)
		V.rule("Witchmail", 2, txt)
		V.rule("Spellbreaker", 2, txt)
		V.rule("Counterspell", 1.5, txt)
		if ngun <= 1 {
			for _, n := range groupDefs[2].names {
				V.penalty(n, 3)
			}
		}
	}
	if len(prof.Tank) >= 2 {
		txt := fmt.Sprintf("%d tank/resist heroes", len(prof.Tank))
		if gunHero {
			V.rule("Bullet Resist Shredder", 2, txt)
			V.rule("Hunter's Aura", 1.5, txt)
		} else {
			V.rule("Mystic Vulnerability", 2, txt)
			V.rule("Escalating Exposure", 1.5, txt)
			V.rule("Tankbuster", 1.5, txt)
		}
	}
	if len(prof.Mobile) >= 3 {
		txt := fmt.Sprintf("%d mobile heroes", len(prof.Mobile))
		V.rule("Slowing Hex", 2, txt)
		V.rule("Knockdown", 1.5, txt)
	}
	if len(prof.Slow) >= 3 {
		V.rule("Debuff Reducer", 1.5, fmt.Sprintf("%d slow heroes", len(prof.Slow)))
	}
	if ms < 25 {
		V.penalty("Healbane", 3)
	}
	if mw < 25 {
		V.penalty("Toxic Bullets", 3)
		V.penalty("Inhibitor", 3)
	}
	return V, prof
}

// ------------------------------------------------------------------ Build logic

type Swap struct {
	Add    string   `json:"add"`
	Remove string   `json:"remove"`
	Zone   string   `json:"zone"`
	Score  float64  `json:"score"`
	Why    string   `json:"why"`
	Notes  []string `json:"notes"`
}

type Scored struct {
	Item  string  `json:"item"`
	Score float64 `json:"score"`
	Why   string  `json:"why"`
}

type Check struct {
	Slots    int    `json:"slots"`
	MaxSlots int    `json:"maxSlots"`
	Actives  int    `json:"actives"`
	Invest   [3]int `json:"invest"`
}

type CalcResp struct {
	Profile  map[string][]string `json:"profile"`
	Swaps    []Swap              `json:"swaps"`
	Fallback []Scored            `json:"fallback"`
	Ranking  []Scored            `json:"ranking"`
	Final    []string            `json:"final"`
	Tips     []string            `json:"tips"`
	Warnings []string            `json:"warnings"`
	Check    Check               `json:"check"`
	Ms       float64             `json:"ms"`
}

func (d *Data) canon(names []string, warn *[]string) []string {
	rs := d.refs(names, warn)
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

// Ref: an item in the build with a precomputed key and a pointer to the item data.
type Ref struct {
	Name string
	Key  string
	It   *Item
}

func (d *Data) refs(names []string, warn *[]string) []Ref {
	out := make([]Ref, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		k := norm(n)
		if it, ok := d.ItemMap[k]; ok {
			out = append(out, Ref{it.Name, k, it})
		} else {
			*warn = append(*warn, "Unknown item (no stats): "+n)
			out = append(out, Ref{n, k, &Item{Name: n, key: k, grp: -1, ci: -1}})
		}
	}
	return out
}

func hasRef(list []Ref, key string) bool {
	for i := range list {
		if list[i].Key == key {
			return true
		}
	}
	return false
}

func finals(refs []Ref) []Ref {
	consumed := map[string]bool{}
	for _, r := range refs {
		for _, c := range r.It.comps {
			consumed[c] = true
		}
	}
	out := make([]Ref, 0, len(refs))
	for _, r := range refs {
		if !consumed[r.Key] {
			out = append(out, r)
		}
	}
	return out
}

func buildStats(build []Ref) (tot [3]int, act int, gc [5]int) {
	for i := range build {
		it := build[i].It
		if it.ci >= 0 {
			tot[it.ci] += it.Cost
		}
		if it.Active {
			act++
		}
		if it.grp >= 0 {
			gc[it.grp]++
		}
	}
	return
}

type kv struct {
	k string
	v float64
}

func sortedScores(V *Votes) []kv {
	out := make([]kv, 0, len(V.score))
	for k, v := range V.score {
		out = append(out, kv{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].v != out[j].v {
			return out[i].v > out[j].v
		}
		return out[i].k < out[j].k
	})
	return out
}

var catNames = [3]string{"Weapon", "Vitality", "Spirit"}

func (d *Data) recommend(me *Hero, enemies []*Hero, lane *Hero, b Build, maxSwaps int) *CalcResp {
	V, prof := computeVotes(me, enemies, lane)
	resp := &CalcResp{Swaps: []Swap{}, Fallback: []Scored{}, Ranking: []Scored{}, Tips: []string{}, Warnings: []string{}, Final: []string{}}
	resp.Profile = map[string][]string{
		"gun": nz(prof.Gun), "spirit": nz(prof.Spirit), "cc": nz(prof.CC), "silence": nz(prof.Silence),
		"heal": nz(prof.Heal), "slow": nz(prof.Slow), "mobile": nz(prof.Mobile), "tank": nz(prof.Tank),
	}
	sorted := sortedScores(V)
	_ = d.refs(b.Early, &resp.Warnings)
	core := d.refs(b.Core, &resp.Warnings)
	late := d.refs(b.Late, &resp.Warnings)
	resp.Tips = earlyTips(lane)

	if len(core) == 0 && len(late) == 0 {
		for _, e := range sorted {
			if e.v < 2.0 || len(resp.Ranking) >= 10 {
				break
			}
			if it := d.ItemMap[e.k]; it != nil {
				resp.Ranking = append(resp.Ranking, Scored{it.Name, round1(e.v), V.reason(e.k)})
			}
		}
		return resp
	}

	cf := finals(core)
	build := make([]Ref, 0, maxSlots+maxSwaps)
	build = append(build, cf...)
	for _, r := range late {
		if len(build) >= maxSlots {
			break
		}
		if !hasRef(build, r.Key) {
			build = append(build, r)
		}
	}

	// Replaceable positions: Late (last first), last 4 Core, middle Core only for a strong reason
	type pe struct {
		ref        Ref
		zone       string
		needStrong bool
	}
	pool := make([]pe, 0, len(core)+len(late))
	for i := len(late) - 1; i >= 0; i-- {
		pool = append(pool, pe{late[i], "Late", false})
	}
	k := len(core)
	for i := k - 1; i >= 0; i-- {
		r := core[i]
		if !hasRef(cf, r.Key) {
			continue
		}
		if i >= k-4 {
			pool = append(pool, pe{r, "Core", false})
		} else if i >= protectedCore {
			pool = append(pool, pe{r, "Core", true})
		}
	}

	// Protect the damage core: for heroes with a clearly dominant category (>= 60 %) at most ONE
	// core item of that category may be replaced; Late items do not count.
	domCi, domSh := 0, me.Shares[0]
	for ci := 1; ci < 3; ci++ {
		if me.Shares[ci] > domSh {
			domCi, domSh = ci, me.Shares[ci]
		}
	}
	capDom := 99
	if domSh >= 60 {
		capDom = 1
	}
	removedDom := 0
	usedAdd := map[string]bool{}
	usedRem := map[string]bool{}
	for round := 0; round < maxSwaps; round++ {
		tot, act, gc := buildStats(build)
		compOfBuild := map[string]bool{}
		for i := range build {
			for _, c := range build[i].It.comps {
				compOfBuild[c] = true
			}
		}
		var bestGain float64
		var best *Swap
		var bestRem string
		bestDom := false
		for _, e := range sorted {
			if e.v < 2.0 {
				break // sorted descending
			}
			if usedAdd[e.k] || compOfBuild[e.k] || hasRef(build, e.k) {
				continue
			}
			cit := d.ItemMap[e.k]
			if cit == nil {
				continue
			}
			strong := e.v >= 5
			for pi := range pool {
				p := &pool[pi]
				if p.needStrong && !strong {
					continue
				}
				rk := p.ref.Key
				if usedRem[rk] || !hasRef(build, rk) {
					continue
				}
				rscore := V.score[rk]
				if rscore >= e.v {
					continue
				}
				rit := p.ref.It
				isDom := p.zone == "Core" && rit.ci == domCi
				if isDom && removedDom >= capDom {
					continue
				}
				if cit.grp >= 0 {
					cnt := gc[cit.grp] + 1
					if rit.grp == cit.grp {
						cnt--
					}
					if cnt > groupDefs[cit.grp].max {
						continue
					}
				}
				na := act
				if rit.Active {
					na--
				}
				if cit.Active {
					na++
				}
				if na > maxActives {
					continue
				}
				after := tot
				if rit.ci >= 0 {
					after[rit.ci] -= rit.Cost
				}
				after[cit.ci] += cit.Cost
				adj := 0.0
				var notes []string
				if rit.ci == cit.ci {
					adj += 0.5
				}
				for ci := 0; ci < 3; ci++ {
					if tot[ci] >= spike && after[ci] < spike {
						adj -= 3
						notes = append(notes, fmt.Sprintf("Warning: %s total drops below the %d spike", catNames[ci], spike))
					} else if tot[ci] < spike && after[ci] >= spike {
						adj += 0.5
						notes = append(notes, fmt.Sprintf("%s total reaches the %d spike", catNames[ci], spike))
					}
				}
				if isDom {
					notes = append(notes, fmt.Sprintf("Replaces a core %s item (%d%% of your investment) - only one such swap is suggested", catNames[domCi], domSh))
				}
				rs := rscore
				if rs < 0 {
					rs = 0
				}
				gain := e.v - 0.8*rs + adj
				if p.zone == "Late" {
					gain += 1.5 // Late items are the most expendable
				} else if p.needStrong {
					gain -= 1.5 // middle of the core: only if clearly worthwhile
				}
				if gain >= minGain && (best == nil || gain > bestGain) {
					bestGain = gain
					best = &Swap{Add: cit.Name, Remove: p.ref.Name, Zone: p.zone, Score: round1(e.v), Why: V.reason(e.k), Notes: notes}
					bestRem = rk
					bestDom = isDom
				}
			}
		}
		if best == nil {
			break
		}
		if best.Notes == nil {
			best.Notes = []string{}
		}
		resp.Swaps = append(resp.Swaps, *best)
		if bestDom {
			removedDom++
		}
		nb := make([]Ref, 0, len(build)+1)
		for _, r := range build {
			if r.Key != bestRem {
				nb = append(nb, r)
			}
		}
		addIt := d.ItemMap[norm(best.Add)]
		build = append(nb, Ref{addIt.Name, addIt.key, addIt})
		usedAdd[addIt.key] = true
		usedRem[bestRem] = true
	}

	for _, e := range sorted {
		if len(resp.Fallback) >= 4 || e.v < 2.0 {
			break
		}
		if it := d.ItemMap[e.k]; it != nil && !hasRef(build, e.k) && !usedAdd[e.k] {
			resp.Fallback = append(resp.Fallback, Scored{it.Name, round1(e.v), V.reason(e.k)})
		}
	}
	for _, r := range build {
		resp.Final = append(resp.Final, r.Name)
	}
	tot, act, _ := buildStats(build)
	resp.Check = Check{Slots: len(build), MaxSlots: maxSlots, Actives: act, Invest: tot}
	return resp
}

func earlyTips(lane *Hero) []string {
	tips := []string{}
	if lane == nil {
		return tips
	}
	w, s := lane.Shares[0], lane.Shares[2]
	if w >= 50 {
		tips = append(tips, fmt.Sprintf("Lane opponent %s is gun-heavy: get early bullet resist (e.g. Battle Vest) or a barrier item.", lane.Name))
	} else if s >= 50 {
		tips = append(tips, fmt.Sprintf("Lane opponent %s is spirit-heavy: get early spirit resist (e.g. Enchanter's Emblem / Restorative Locket).", lane.Name))
	}
	if lane.isHeal {
		tips = append(tips, fmt.Sprintf("%s has a lot of sustain: plan Healbane/Toxic Bullets early.", lane.Name))
	}
	if lane.isCC {
		tips = append(tips, fmt.Sprintf("%s has hard CC: Debuff Reducer is cheap (T2) and helps in lane.", lane.Name))
	}
	return tips
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

// ------------------------------------------------------------------ Defaults (saved builds)

type Store struct {
	mu   sync.Mutex
	path string
	m    map[string]Build
}

func newStore() *Store {
	p := "defaults.json"
	if exe, err := os.Executable(); err == nil && !strings.Contains(exe, "go-build") {
		p = filepath.Join(filepath.Dir(exe), "defaults.json")
	}
	s := &Store{path: p, m: map[string]Build{}}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &s.m)
	}
	return s
}

func (s *Store) save() error {
	b, err := json.MarshalIndent(s.m, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) snapshot() map[string]Build {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := make(map[string]Build, len(s.m))
	for k, v := range s.m {
		c[k] = v
	}
	return c
}

// ------------------------------------------------------------------ HTTP

type CalcReq struct {
	Me      string   `json:"me"`
	Enemies []string `json:"enemies"`
	Lane    string   `json:"lane"`
	Swaps   int      `json:"swaps"`
	Build   *Build   `json:"build"`
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	port := flag.Int("port", 47871, "port (a free one is picked automatically if it is taken)")
	noBrowser := flag.Bool("nobrowser", false, "do not open the browser automatically")
	noIcons := flag.Bool("no-icons", false, "do not download icons (disables all network access)")
	noUpdate := flag.Bool("no-update", false, "do not check for program updates")
	assetsAPI := flag.String("assets-api", "https://api.deadlock-api.com", "base URL of the asset API")
	flag.Parse()

	netEnabled = !*noIcons
	if netEnabled && !*noUpdate {
		selfUpdate()
	}
	loadSettings()
	t0 := time.Now()
	d, err := loadData()
	if err != nil {
		log.Fatalf("Could not load data: %v", err)
	}
	store := newStore()
	log.Printf("Data loaded in %v: %d heroes, %d items (%s)", time.Since(t0), len(d.Heroes), len(d.Items), d.Source)

	icons := newIcons(d, *assetsAPI, !*noIcons)
	if icons.enabled && icons.missing() > 0 {
		icons.start(false)
	}

	syncLive(d, icons)
	skills = newSkills(*assetsAPI, !*noIcons)
	if skills.enabled && skills.stale() {
		skills.start(false)
	}

	ui, _ := assets.ReadFile("ui.html")
	mux := http.NewServeMux()
	mux.Handle("/icons/", http.StripPrefix("/icons/", cacheFor(http.FileServer(http.Dir(icons.dir)), "public, max-age=86400")))
	mux.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("deadlock-counter"))
	})
	mux.HandleFunc("/api/icons", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, icons.status())
	})
	mux.HandleFunc("/api/icons/refresh", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-DL") != "1" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		icons.scan()
		writeJSON(w, map[string]any{"started": icons.start(true)})
	})
	favicon, _ := assets.ReadFile("assets/icon.ico")
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/x-icon")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(favicon)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(ui)
	})
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"meta": d.Meta, "heroes": d.Heroes, "items": d.Items,
			"defaults": store.snapshot(), "source": d.Source})
	})
	mux.HandleFunc("/api/calc", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-DL") != "1" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var req CalcReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		start := time.Now()
		me := d.HeroMap[norm(req.Me)]
		if me == nil {
			http.Error(w, "unknown hero", http.StatusBadRequest)
			return
		}
		var enemies []*Hero
		var lane *Hero
		for _, n := range req.Enemies {
			if h := d.HeroMap[norm(n)]; h != nil {
				enemies = append(enemies, h)
				if req.Lane != "" && norm(req.Lane) == norm(n) {
					lane = h
				}
			}
		}
		if req.Swaps <= 0 || req.Swaps > 8 {
			req.Swaps = 3
		}
		var b Build
		if req.Build != nil {
			b = *req.Build
		} else {
			b = me.Catalog
		}
		resp := d.recommend(me, enemies, lane, b, req.Swaps)
		resp.Ms = float64(time.Since(start).Microseconds()) / 1000
		writeJSON(w, resp)
	})
	mux.HandleFunc("/api/export", d.exportHandler)
	mux.HandleFunc("/api/sync", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": syncStatus, "tracklock": tlStatus})
	})
	mux.HandleFunc("/api/skills", func(w http.ResponseWriter, r *http.Request) {
		_, info := skills.seqFor(norm(r.URL.Query().Get("hero")))
		writeJSON(w, map[string]any{"info": info, "status": skills.Status()})
	})
	mux.HandleFunc("/api/skills/refresh", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-DL") != "1" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"started": skills.start(true)})
	})
	mux.HandleFunc("/api/default", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-DL") != "1" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodPost:
			var body struct {
				Name  string `json:"name"`
				Build Build  `json:"build"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || d.HeroMap[norm(body.Name)] == nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			var warn []string
			body.Build = Build{Early: d.canon(body.Build.Early, &warn), Core: d.canon(body.Build.Core, &warn), Late: d.canon(body.Build.Late, &warn)}
			store.mu.Lock()
			store.m[d.HeroMap[norm(body.Name)].Name] = body.Build
			err := store.save()
			store.mu.Unlock()
			if err != nil {
				http.Error(w, "Speichern fehlgeschlagen: "+err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]any{"ok": true, "path": store.path})
		case http.MethodDelete:
			name := r.URL.Query().Get("name")
			h := d.HeroMap[norm(name)]
			if h == nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			store.mu.Lock()
			delete(store.m, h.Name)
			err := store.save()
			store.mu.Unlock()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]any{"ok": true})
		default:
			http.Error(w, "bad request", http.StatusMethodNotAllowed)
		}
	})

	// Start on a port and verify via self-check that THIS program is the one answering
	// (on Windows two programs can share a port).
	for _, p := range []int{*port, 0} {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			log.Printf("Port %d not usable: %v", p, err)
			continue
		}
		srv := &http.Server{Handler: mux}
		errCh := make(chan error, 1)
		go func() { errCh <- srv.Serve(ln) }()
		url := "http://" + ln.Addr().String()
		if !selfCheck(url) {
			log.Printf("Port %d is shared with another program - using a different port", ln.Addr().(*net.TCPAddr).Port)
			_ = srv.Close()
			continue
		}
		log.Printf("Deadlock Counter-Build running on %s  (close this window to quit)", url)
		if !*noBrowser {
			go openBrowser(url)
		}
		log.Fatal(<-errCh)
	}
	log.Fatal("no free port found")
}

func selfCheck(url string) bool {
	c := &http.Client{Timeout: 1500 * time.Millisecond}
	for i := 0; i < 5; i++ {
		resp, err := c.Get(url + "/api/ping")
		if err == nil {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
			resp.Body.Close()
			return string(b) == "deadlock-counter"
		}
		time.Sleep(60 * time.Millisecond)
	}
	return false
}

func cacheFor(h http.Handler, cc string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", cc)
		h.ServeHTTP(w, r)
	})
}

func openBrowser(u string) {
	time.Sleep(200 * time.Millisecond)
	switch runtime.GOOS {
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	case "darwin":
		_ = exec.Command("open", u).Start()
	default:
		_ = exec.Command("xdg-open", u).Start()
	}
}
