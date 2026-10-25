package main

// Live builds from tracklock.gg: at startup (at most every 24 h) each hero's build page is loaded,
// Core/Early/Late are read in order from the item images and replace the built-in snapshot.
// Safety net: if a page is not recognised cleanly (unknown items, too few core items,
// network error), the snapshot for that hero stays unchanged.

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var netEnabled bool // set in main (false with -no-icons and in tests)

type tlBuild struct {
	Core, Early, Late []string
	WR                float64
	Matches           int
	At                int64
}

var tlStatus = "Tracklock sync: not run"

var (
	reTok     = regexp.MustCompile(`(?is)<img\b[^>]*>|>([^<>]+)<`)
	reAttrSrc = regexp.MustCompile(`(?is)\b(?:src|srcset|data-src)\s*=\s*"([^"]+)"`)
	reAttrAlt = regexp.MustCompile(`(?is)\b(?:alt|title)\s*=\s*"([^"]*)"`)
	reSlug    = regexp.MustCompile(`/images/items/[a-z_]+/([a-z0-9_]+)\.(?:webp|png|jpg|svg)`)
	reSection = regexp.MustCompile(`(?i)^(core(?: build)?|early(?: game)?|late(?: game)?|defensives?|counter picks?|actives?)\b`)
	reWR      = regexp.MustCompile(`(?i)(\d{2}(?:\.\d+)?)\s*%\s*(?:win rate|wr)?`)
	reMatches = regexp.MustCompile(`(?i)([\d.,]+)\s*(?:matches|games)`)
)

func heroSlugs(name string) []string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.ReplaceAll(n, "&", " and ")
	clean := func(s string) string {
		s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
		return strings.Trim(s, "-")
	}
	c := []string{clean(n)}
	c = append(c, clean(strings.ReplaceAll(n, " and ", " ")), clean(strings.TrimPrefix(n, "the ")), clean(strings.ReplaceAll(n, "'", "")))
	if strings.Contains(n, "krill") {
		c = append(c, "mo-krill", "mo-and-krill")
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range c {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// itemResolver: image slug / alt text / name -> Item
func (d *Data) resolveItem(alt, slug string) *Item {
	for _, k := range []string{norm(alt), norm(slug)} {
		if k == "" {
			continue
		}
		if it := d.ItemMap[k]; it != nil {
			return it
		}
	}
	for _, it := range d.Items {
		in := strings.TrimPrefix(it.IName, "upgrade_")
		if slug != "" && (in == slug || strings.TrimPrefix(in, "citadel_") == slug) {
			return it
		}
	}
	return nil
}

// parseTracklockHTML reads the sections of a build page's HTML. Returns unresolved items.
func parseTracklockHTML(d *Data, page string) (b tlBuild, unresolved []string) {
	section := ""
	seen := map[string]bool{}
	add := func(sec string, it *Item) {
		k := sec + "|" + it.key
		if seen[k] {
			return
		}
		seen[k] = true
		switch sec {
		case "core":
			b.Core = append(b.Core, it.Name)
		case "early":
			b.Early = append(b.Early, it.Name)
		case "late":
			b.Late = append(b.Late, it.Name)
		}
	}
	for _, m := range reTok.FindAllStringSubmatch(page, -1) {
		if m[1] != "" { // text node
			t := strings.TrimSpace(html.UnescapeString(m[1]))
			if len(t) > 40 {
				continue
			}
			if sm := reSection.FindStringSubmatch(t); sm != nil {
				w := strings.ToLower(sm[1])
				switch {
				case strings.HasPrefix(w, "core"):
					section = "core"
				case strings.HasPrefix(w, "early"):
					section = "early"
				case strings.HasPrefix(w, "late"):
					section = "late"
				default:
					section = "other"
				}
			}
			if b.WR == 0 {
				if wm := reWR.FindStringSubmatch(t); wm != nil && strings.Contains(strings.ToLower(t), "win") {
					b.WR, _ = strconv.ParseFloat(wm[1], 64)
				}
			}
			continue
		}
		tag := m[0]
		if section == "" || section == "other" {
			continue
		}
		dec := tag
		if u, err := url.QueryUnescape(tag); err == nil {
			dec = u
		}
		sm := reSlug.FindStringSubmatch(dec)
		alt := ""
		if am := reAttrAlt.FindStringSubmatch(tag); am != nil {
			alt = html.UnescapeString(am[1])
		}
		slug := ""
		if sm != nil {
			slug = sm[1]
		} else if src := reAttrSrc.FindStringSubmatch(tag); src == nil || !strings.Contains(src[1], "item") {
			continue
		}
		if slug == "" && alt == "" {
			continue
		}
		if it := d.resolveItem(alt, slug); it != nil {
			add(section, it)
		} else {
			unresolved = append(unresolved, strings.TrimSpace(alt+" "+slug))
		}
	}
	return
}

func tlValid(b tlBuild) bool { return len(b.Core) >= 8 && len(b.Core) <= 18 }

func tlMix(d *Data, core []string) (mix [3]int, ok bool) {
	var t [3]int
	for _, n := range core {
		if it := d.ItemMap[norm(n)]; it != nil && it.ci >= 0 {
			t[it.ci] += it.Cost
		}
	}
	s := t[0] + t[1] + t[2]
	if s == 0 {
		return mix, false
	}
	for i := range mix {
		mix[i] = int(float64(t[i])*100/float64(s) + 0.5)
	}
	return mix, true
}

var tlClient = &http.Client{Timeout: 8 * time.Second}

func tlFetch(slug string) (string, error) {
	req, _ := http.NewRequest("GET", "https://tracklock.gg/heroes/"+slug+"/build", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; DeadlockCounterBuild/1.0; hobby project)")
	req.Header.Set("Accept", "text/html")
	resp, err := tlClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return string(b), err
}

// applyTracklock: load the cache, fetch fresh data if needed, apply it to the heroes. Must run BEFORE deriveMissing.
func applyTracklock(d *Data) {
	path := filepath.Join(appDir(), "tracklock_cache.json")
	cache := map[string]tlBuild{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &cache)
	}
	fresh := len(cache) > 0
	for _, e := range cache {
		if time.Now().Unix()-e.At > 24*3600 {
			fresh = false
		}
		break
	}
	note := "Cache"
	if netEnabled && !fresh {
		var mu sync.Mutex
		var wg sync.WaitGroup
		sem := make(chan struct{}, 6)
		deadline := time.Now().Add(30 * time.Second)
		ok, fail := 0, 0
		var firstErr string
		for _, h := range d.Heroes {
			wg.Add(1)
			sem <- struct{}{}
			go func(h *Hero) {
				defer wg.Done()
				defer func() { <-sem }()
				var lastErr string
				for _, slug := range heroSlugs(h.Name) {
					if time.Now().After(deadline) {
						lastErr = "Zeitlimit"
						break
					}
					page, err := tlFetch(slug)
					if err != nil {
						lastErr = fmt.Sprintf("%s: %v", slug, err)
						if strings.Contains(err.Error(), "404") {
							continue
						}
						break
					}
					b, unres := parseTracklockHTML(d, page)
					if !tlValid(b) {
						lastErr = fmt.Sprintf("%s: page not recognised (%d core items, unknown: %s)", slug, len(b.Core), strings.Join(firstN(unres, 3), ", "))
						mu.Lock()
						if firstErr == "" {
							_ = os.WriteFile(filepath.Join(appDir(), "tracklock_debug.html"), []byte(page), 0644)
						}
						mu.Unlock()
						continue
					}
					if len(unres) > 0 {
						log.Printf("Tracklock %s: unknown items ignored: %s", h.Name, strings.Join(unres, ", "))
					}
					b.At = time.Now().Unix()
					if wm := reMatches.FindStringSubmatch(page); wm != nil {
						n, _ := strconv.Atoi(strings.NewReplacer(",", "", ".", "").Replace(wm[1]))
						b.Matches = n
					}
					mu.Lock()
					cache[h.Name] = b
					ok++
					mu.Unlock()
					return
				}
				mu.Lock()
				fail++
				if firstErr == "" {
					firstErr = h.Name + " – " + lastErr
				}
				mu.Unlock()
			}(h)
		}
		wg.Wait()
		if ok > 0 {
			if b, err := json.MarshalIndent(cache, "", " "); err == nil {
				_ = os.WriteFile(path, b, 0644)
			}
			note = "live"
		}
		if ok == 0 {
			tlStatus = "Tracklock sync unavailable (" + shortErr(fmt.Errorf("%s", firstErr)) + ") - using built-in snapshot"
			if len(cache) == 0 {
				return
			}
		} else if fail > 0 {
			tlStatus = fmt.Sprintf("Tracklock sync: %d heroes live, %d unchanged (%s)", ok, fail, shortErr(fmt.Errorf("%s", firstErr)))
		}
	}
	if len(cache) == 0 {
		return
	}
	n := 0
	for _, h := range d.Heroes {
		b, ok := cache[h.Name]
		if !ok || !tlValid(b) {
			continue
		}
		h.Catalog.Core = b.Core
		if len(b.Early) > 0 {
			h.Catalog.Early = b.Early
		}
		if len(b.Late) > 0 {
			h.Catalog.Late = b.Late
		}
		if m, ok := tlMix(d, b.Core); ok {
			h.mix, h.hasMix, h.Shares = m, true, m
		}
		if b.WR > 0 {
			h.WR = b.WR
		}
		h.HasBuild, h.Derived = true, false
		n++
	}
	if n > 0 && strings.HasPrefix(tlStatus, "Tracklock sync: not") {
		tlStatus = fmt.Sprintf("Tracklock sync (%s): %d builds", note, n)
	}
	log.Print(tlStatus)
}
