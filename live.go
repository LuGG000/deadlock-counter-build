package main

// Live sync: keeps items (cost/tier, new items) and hero IDs/ability IDs up to date
// from the asset API (same source as the icons). Cache: live_cache.json next to the exe (24h).
// Runs once at startup before the server handles requests (no locking needed).

import (
	"encoding/json"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type liveItem struct {
	Class string `json:"class"`
	Name  string `json:"name"`
	Tier  int    `json:"tier"`
	Cost  int    `json:"cost"`
	Slot  string `json:"slot"` // W / V / S
	Tip   string `json:"tip"`  // stats and description for the hover tooltip
}
type liveHero struct {
	ID    uint32   `json:"id"`
	Class string   `json:"class"`
	Name  string   `json:"name"`
	Abil  []string `json:"abil"` // Klassennamen Signature 1-4
}
type liveCache struct {
	At     int64      `json:"at"`
	Items  []liveItem `json:"items"`
	Heroes []liveHero `json:"heroes"`
}

var syncStatus = "Live sync: not run"

var (
	reDigits = regexp.MustCompile(`\d+`)
	reTags   = regexp.MustCompile(`(?s)<[^>]*>`)
)

// liveTip builds the hover text like the snapshot does: "Label value, ..." | description.
func liveTip(o map[string]any) string {
	var stats []string
	if props, ok := o["properties"].(map[string]any); ok {
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		seen := map[string]bool{}
		for _, k := range keys {
			p, _ := props[k].(map[string]any)
			label, _ := p["label"].(string)
			val := strings.TrimSpace(fmt.Sprint(p["value"]))
			if label == "" || label == "Charge Delay" || val == "" || val == "0" || val == "<nil>" {
				continue
			}
			if post, _ := p["postfix"].(string); post != "" && !strings.HasSuffix(val, post) {
				val += post
			}
			if s := label + " " + val; !seen[s] {
				seen[s] = true
				stats = append(stats, s)
			}
		}
	}
	tip := strings.Join(stats, ", ")
	if desc, ok := o["description"].(map[string]any); ok {
		for _, k := range []string{"desc", "passive", "active"} {
			s, _ := desc[k].(string)
			s = strings.Join(strings.Fields(html.UnescapeString(reTags.ReplaceAllString(s, ""))), " ")
			if s == "" {
				continue
			}
			if r := []rune(s); len(r) > 240 {
				s = string(r[:240]) + "…"
			}
			if tip != "" {
				tip += " | "
			}
			return tip + s
		}
	}
	return tip
}

func anyInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		if m := reDigits.FindAllString(t, -1); len(m) > 0 {
			n, _ := strconv.Atoi(m[len(m)-1])
			return n
		}
	}
	return 0
}
func anyStr(o map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := o[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
func slotOf(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "weapon"):
		return "W"
	case strings.Contains(s, "armor"), strings.Contains(s, "vital"):
		return "V"
	case strings.Contains(s, "tech"), strings.Contains(s, "spirit"):
		return "S"
	}
	return ""
}

func parseLiveItems(list []map[string]any) []liveItem {
	var out []liveItem
	for _, o := range list {
		cls := anyStr(o, "class_name", "className")
		if !strings.HasPrefix(cls, "upgrade_") {
			continue
		}
		if b, ok := o["disabled"].(bool); ok && b {
			continue
		}
		it := liveItem{Class: cls, Name: anyStr(o, "name"), Slot: slotOf(anyStr(o, "item_slot_type", "slot_type", "slot")), Tip: liveTip(o)}
		for _, k := range []string{"item_tier", "tier"} {
			if v, ok := o[k]; ok && anyInt(v) > 0 {
				it.Tier = anyInt(v)
				break
			}
		}
		for _, k := range []string{"cost", "item_cost", "cost_souls"} {
			if v, ok := o[k]; ok && anyInt(v) > 0 {
				it.Cost = anyInt(v)
				break
			}
		}
		out = append(out, it)
	}
	return out
}

func parseLiveHeroes(list []map[string]any) []liveHero {
	var out []liveHero
	for _, o := range list {
		h := liveHero{ID: uint32(anyInt(o["id"])), Class: anyStr(o, "class_name", "className"), Name: anyStr(o, "name")}
		if items, ok := o["items"].(map[string]any); ok {
			for i := 1; i <= 4; i++ {
				s, _ := items["signature"+strconv.Itoa(i)].(string)
				h.Abil = append(h.Abil, s)
			}
		}
		if h.Name != "" && h.ID > 0 {
			out = append(out, h)
		}
	}
	return out
}

// syncLive loads (or takes the fresh cache) and applies the data.
func syncLive(d *Data, ic *Icons) {
	path := filepath.Join(appDir(), "live_cache.json")
	var c liveCache
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	// A cache written before tooltips were synced has no Tip yet and counts as stale.
	fresh := c.At > 0 && time.Now().Unix()-c.At < 24*3600 && len(c.Items) > 0 && c.Items[0].Tip != ""
	note := "Cache"
	if !fresh && ic != nil && ic.enabled {
		old := ic.client.Timeout
		ic.client.Timeout = 8 * time.Second
		il, e1 := ic.fetchKind("items")
		hl, e2 := ic.fetchKind("heroes")
		ic.client.Timeout = old
		if e1 == nil && e2 == nil {
			c = liveCache{At: time.Now().Unix(), Items: parseLiveItems(il), Heroes: parseLiveHeroes(hl)}
			if b, err := json.Marshal(c); err == nil && len(c.Items) > 0 {
				_ = os.WriteFile(path, b, 0644)
			}
			note = "live"
		} else if len(c.Items) == 0 {
			err := e1
			if err == nil {
				err = e2
			}
			syncStatus = "Live sync unavailable (" + shortErr(err) + ") - using built-in data"
			return
		} else {
			note = "Cache (offline)"
		}
	}
	if len(c.Items) == 0 {
		syncStatus = "Live sync off"
		return
	}
	byClass := map[string]*Item{}
	for _, it := range d.Items {
		if it.IName != "" {
			byClass[it.IName] = it
		}
	}
	upd, add := 0, 0
	var added []string
	for _, li := range c.Items {
		if it := byClass[li.Class]; it != nil {
			ch := false
			if li.Cost > 0 && li.Cost != it.Cost {
				it.Cost, ch = li.Cost, true
			}
			if li.Tier > 0 && li.Tier != it.Tier {
				it.Tier, ch = li.Tier, true
			}
			if ch {
				upd++
			}
			if li.Tip != "" {
				it.Tip = li.Tip
			}
			continue
		}
		if li.Name == "" || li.Tier == 0 || li.Cost == 0 || li.Slot == "" {
			continue
		}
		key := norm(li.Name)
		if _, dup := d.ItemMap[key]; dup {
			continue
		}
		cat := map[string]string{"W": "Weapon", "V": "Vitality", "S": "Spirit"}[li.Slot]
		it := &Item{Name: li.Name, IName: li.Class, Cat: cat, Tier: li.Tier, Cost: li.Cost, key: key, grp: -1, ci: -1,
			Tip: "New item (loaded automatically) - not yet in the scoring"}
		if li.Tip != "" {
			it.Tip = li.Tip + " | new item, not yet in the scoring"
		}
		if ci, ok := catIdx[cat]; ok {
			it.ci = ci
		}
		d.Items = append(d.Items, it)
		d.ItemMap[key] = it
		byClass[li.Class] = it
		add++
		added = append(added, li.Name)
	}
	// Hero IDs: existing ones are NEVER overwritten (protects against a misread format); only missing ones are added.
	idKnown := map[uint32]bool{}
	for _, id := range heroIDs {
		idKnown[id] = true
	}
	idUpd, abUpd := 0, 0
	var newHeroes []string
	for _, lh := range c.Heroes {
		k := norm(lh.Name)
		_, inCat := d.HeroMap[k]
		if _, ok := heroIDs[k]; !ok && inCat && !idKnown[lh.ID] {
			heroIDs[k] = lh.ID
			idUpd++
		}
		if !inCat && !idKnown[lh.ID] && !strings.Contains(lh.Class, "base") && !strings.Contains(lh.Class, "test") {
			newHeroes = append(newHeroes, lh.Name)
		}
		// Abilities: only adopt if at least one ID matches a known one (format confirmed)
		if ha, ok := heroAbil[k]; ok && len(lh.Abil) == 4 {
			var ids [4]uint32
			match, all := 0, true
			for i, s := range lh.Abil {
				if s == "" {
					all = false
					break
				}
				ids[i] = murmur2(s)
				if ids[i] == ha.ID[i] {
					match++
				}
			}
			if all && match > 0 && ids != ha.ID {
				ha.ID = ids
				heroAbil[k] = ha
				abUpd++
			}
		}
	}
	msg := fmt.Sprintf("Live sync (%s): %d items updated, %d new", note, upd, add)
	if len(added) > 0 {
		msg += " (" + strings.Join(firstN(added, 5), ", ") + ")"
	}
	if idUpd+abUpd > 0 {
		msg += fmt.Sprintf(", %d hero IDs/%d abilities updated", idUpd, abUpd)
	}
	if len(newHeroes) > 0 {
		msg += " · new heroes without catalog data: " + strings.Join(firstN(newHeroes, 6), ", ")
	}
	syncStatus = msg
	log.Print(msg)
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
func shortErr(e error) string {
	s := fmt.Sprint(e)
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}
