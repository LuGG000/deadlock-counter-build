package main

// Icons: downloads hero and item icons once from the community asset API
// (assets.deadlock-api.com) and stores them in the "icons" folder next to the exe.
// After that everything works offline. Custom images can simply be placed in icons/heroes or
// icons/items (file name = name without special characters, e.g. abrams.png).

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const bucketHost = "assets-bucket.deadlock-api.com"

var (
	itemURLPrefs = []string{"image_webp", "image", "shop_image_webp", "shop_image"}
	heroURLPrefs = []string{"icon_image_small_webp", "icon_image_small", "icon_hero_card_webp", "icon_hero_card",
		"selection_image_webp", "selection_image", "minimap_image_webp", "minimap_image"}
	imgExts = map[string]bool{".webp": true, ".png": true, ".jpg": true, ".jpeg": true, ".svg": true}
)

type Icons struct {
	dir     string
	d       *Data
	api     string
	apiHost string
	enabled bool
	client  *http.Client

	mu                  sync.Mutex
	heroes              map[string]string
	items               map[string]string
	running             bool
	total, done, failed int
	lastErr             string
	lastTry             time.Time
}

func appDir() string {
	if exe, err := os.Executable(); err == nil && !strings.Contains(exe, "go-build") {
		return filepath.Dir(exe)
	}
	wd, _ := os.Getwd()
	return wd
}

func newIcons(d *Data, api string, enabled bool) *Icons {
	u, _ := url.Parse(api)
	ic := &Icons{dir: filepath.Join(appDir(), "icons"), d: d, api: strings.TrimRight(api, "/"), enabled: enabled,
		client: &http.Client{Timeout: 25 * time.Second}, heroes: map[string]string{}, items: map[string]string{}}
	if u != nil {
		ic.apiHost = u.Host
	}
	if enabled {
		for _, k := range []string{"heroes", "items"} {
			if err := os.MkdirAll(filepath.Join(ic.dir, k), 0o755); err != nil {
				ic.enabled = false
				ic.lastErr = "Icon folder not writable: " + err.Error()
			}
		}
	}
	ic.scan()
	return ic
}

func scanDir(dir string) map[string]string {
	m := map[string]string{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if e.IsDir() || !imgExts[ext] || strings.HasPrefix(name, "_") {
			continue
		}
		m[strings.TrimSuffix(name, filepath.Ext(name))] = name
	}
	return m
}

func heroKeys(name string) []string {
	n := strings.TrimSpace(name)
	vs := []string{n, strings.ReplaceAll(n, "&", "and")}
	if strings.HasPrefix(strings.ToLower(n), "the ") {
		vs = append(vs, n[4:])
	}
	var out []string
	seen := map[string]bool{}
	for _, v := range vs {
		if k := norm(v); k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// scan re-reads the existing files.
func (ic *Icons) scan() {
	hs, is := scanDir(filepath.Join(ic.dir, "heroes")), scanDir(filepath.Join(ic.dir, "items"))
	h, i := map[string]string{}, map[string]string{}
	for _, hero := range ic.d.Heroes {
		for _, k := range heroKeys(hero.Name) {
			if f, ok := hs[k]; ok {
				h[hero.Name] = "heroes/" + f
				break
			}
		}
	}
	for _, it := range ic.d.Items {
		if f, ok := is[it.key]; ok {
			i[it.Name] = "items/" + f
		}
	}
	ic.mu.Lock()
	ic.heroes, ic.items = h, i
	ic.mu.Unlock()
}

func (ic *Icons) missing() int {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	return (len(ic.d.Heroes) - len(ic.heroes)) + (len(ic.d.Items) - len(ic.items))
}

func (ic *Icons) status() map[string]any {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	h, i := make(map[string]string, len(ic.heroes)), make(map[string]string, len(ic.items))
	for k, v := range ic.heroes {
		h[k] = v
	}
	for k, v := range ic.items {
		i[k] = v
	}
	return map[string]any{
		"enabled": ic.enabled, "running": ic.running, "total": ic.total, "done": ic.done, "failed": ic.failed,
		"err": ic.lastErr, "heroes": h, "items": i,
		"heroTotal": len(ic.d.Heroes), "itemTotal": len(ic.d.Items),
	}
}

func (ic *Icons) setErr(s string) {
	ic.mu.Lock()
	ic.lastErr = s
	ic.mu.Unlock()
	log.Printf("Icons: %s", s)
}

func (ic *Icons) start(force bool) bool {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if !ic.enabled || ic.running {
		return false
	}
	if !force && !ic.lastTry.IsZero() && time.Since(ic.lastTry) < time.Hour {
		return false
	}
	ic.running = true
	ic.total, ic.done, ic.failed = 0, 0, 0
	ic.lastErr = ""
	ic.lastTry = time.Now()
	go ic.run()
	return true
}

type iconTask struct {
	kind, name, key, url string
}

func (ic *Icons) allowed(u *url.URL) bool {
	if u == nil {
		return false
	}
	h := u.Hostname()
	loop := h == "127.0.0.1" || h == "localhost" || h == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && loop) {
		return false
	}
	return h == bucketHost || strings.HasSuffix(h, ".deadlock-api.com") || (ic.apiHost != "" && u.Host == ic.apiHost)
}

func (ic *Icons) request(rawURL string) (*http.Response, error) {
	u, err := url.Parse(rawURL)
	if err != nil || !ic.allowed(u) {
		return nil, fmt.Errorf("URL not allowed: %s", rawURL)
	}
	req, _ := http.NewRequest("GET", rawURL, nil)
	req.Header.Set("User-Agent", "DeadlockCounterBuild/1.0 (local hobby project)")
	req.Header.Set("Referer", "https://deadlock-api.com/")
	resp, err := ic.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// fetchKind tries the known asset endpoints in turn (new: api.../v1/assets/<kind>, old: assets.../v2/<kind>).
func (ic *Icons) fetchKind(kind string) ([]map[string]any, error) {
	cands := []string{ic.api + "/v1/assets/" + kind, ic.api + "/v2/" + kind, "https://assets.deadlock-api.com/v2/" + kind}
	seen := map[string]bool{}
	var errs []string
	for _, u := range cands {
		if seen[u] {
			continue
		}
		seen[u] = true
		list, err := ic.fetchList(u)
		if err == nil && len(list) > 0 {
			log.Printf("Icons: %s von %s", kind, u)
			return list, nil
		}
		if err == nil {
			err = fmt.Errorf("empty list")
		}
		errs = append(errs, fmt.Sprintf("%s -> %v", u, err))
	}
	return nil, fmt.Errorf("%s", strings.Join(errs, " | "))
}

func (ic *Icons) fetchList(full string) ([]map[string]any, error) {
	resp, err := ic.request(full)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var v any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 40<<20)).Decode(&v); err != nil {
		return nil, err
	}
	toList := func(a []any) []map[string]any {
		var out []map[string]any
		for _, x := range a {
			if m, ok := x.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	switch t := v.(type) {
	case []any:
		return toList(t), nil
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if a, ok := t[k].([]any); ok {
				return toList(a), nil
			}
		}
	}
	return nil, fmt.Errorf("unexpected JSON format")
}

func imageURL(s string) bool {
	if !strings.HasPrefix(s, "http") {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && imgExts[strings.ToLower(path.Ext(u.Path))]
}

// pickURL picks the matching image URL from an API object (including nested objects such as "images").
func pickURL(o map[string]any, prefs []string) string {
	flat := map[string]string{}
	add := func(m map[string]any) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if s, ok := m[k].(string); ok && imageURL(s) {
				if _, dup := flat[k]; !dup {
					flat[k] = s
				}
			}
		}
	}
	add(o)
	var nested []string
	for k, v := range o {
		if _, ok := v.(map[string]any); ok {
			nested = append(nested, k)
		}
	}
	sort.Strings(nested)
	for _, k := range nested {
		add(o[k].(map[string]any))
	}
	for _, p := range prefs {
		if u, ok := flat[p]; ok {
			return u
		}
	}
	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "image") || strings.Contains(lk, "icon") {
			return flat[k]
		}
	}
	return ""
}

func (ic *Icons) debugDump(kind string, objs []map[string]any) {
	n := 2
	if len(objs) < n {
		n = len(objs)
	}
	b, _ := json.MarshalIndent(objs[:n], "", "  ")
	_ = os.WriteFile(filepath.Join(ic.dir, "_debug_"+kind+".json"), b, 0o644)
}

func (ic *Icons) run() {
	defer func() {
		ic.mu.Lock()
		ic.running = false
		ic.mu.Unlock()
		ic.scan()
	}()
	var tasks []iconTask
	haveI := scanDir(filepath.Join(ic.dir, "items"))
	haveH := scanDir(filepath.Join(ic.dir, "heroes"))

	itemObjs, err := ic.fetchKind("items")
	if err != nil {
		ic.setErr("Item list unavailable: " + err.Error())
	} else {
		byName := map[string]map[string]any{}
		for _, o := range itemObjs {
			if n, _ := o["name"].(string); n != "" {
				if _, ok := byName[norm(n)]; !ok {
					byName[norm(n)] = o
				}
			}
		}
		matched, withURL := 0, 0
		for _, it := range ic.d.Items {
			if _, ok := haveI[it.key]; ok {
				continue
			}
			o := byName[it.key]
			if o == nil {
				continue
			}
			matched++
			if u := pickURL(o, itemURLPrefs); u != "" {
				withURL++
				tasks = append(tasks, iconTask{"items", it.Name, it.key, u})
			}
		}
		log.Printf("Icons: items - API returned %d entries, %d matched, %d with image URL", len(itemObjs), matched, withURL)
		if matched > 0 && withURL == 0 {
			ic.debugDump("items", itemObjs)
			ic.setErr("No item image URLs found (see icons/_debug_items.json)")
		}
	}

	heroObjs, err := ic.fetchKind("heroes")
	if err != nil {
		ic.setErr("Hero list unavailable: " + err.Error())
	} else {
		byName := map[string]map[string]any{}
		for _, o := range heroObjs {
			if n, _ := o["name"].(string); n != "" {
				for _, k := range heroKeys(n) {
					if _, ok := byName[k]; !ok {
						byName[k] = o
					}
				}
			}
		}
		matched, withURL := 0, 0
		for _, h := range ic.d.Heroes {
			var o map[string]any
			skip := false
			for _, k := range heroKeys(h.Name) {
				if _, ok := haveH[k]; ok {
					skip = true
				}
				if o == nil {
					o = byName[k]
				}
			}
			if skip || o == nil {
				continue
			}
			matched++
			u := pickURL(o, heroURLPrefs)
			if u == "" {
				if cn, _ := o["class_name"].(string); strings.HasPrefix(cn, "hero_") {
					u = "https://" + bucketHost + "/assets-api-res/images/heroes/" + strings.TrimPrefix(cn, "hero_") + "_sm.webp"
				}
			}
			if u != "" {
				withURL++
				tasks = append(tasks, iconTask{"heroes", h.Name, norm(h.Name), u})
			}
		}
		log.Printf("Icons: heroes - API returned %d entries, %d matched, %d with image URL", len(heroObjs), matched, withURL)
		if matched > 0 && withURL == 0 {
			ic.debugDump("heroes", heroObjs)
			ic.setErr("No hero image URLs found (see icons/_debug_heroes.json)")
		}
	}

	ic.mu.Lock()
	ic.total = len(tasks)
	ic.mu.Unlock()
	if len(tasks) == 0 {
		return
	}
	ch := make(chan iconTask)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range ch {
				rel, err := ic.download(t)
				ic.mu.Lock()
				if err != nil {
					ic.failed++
					if ic.lastErr == "" {
						ic.lastErr = fmt.Sprintf("%s: %v", t.name, err)
					}
				} else {
					ic.done++
					if t.kind == "heroes" {
						ic.heroes[t.name] = rel
					} else {
						ic.items[t.name] = rel
					}
				}
				ic.mu.Unlock()
			}
		}()
	}
	for _, t := range tasks {
		ch <- t
	}
	close(ch)
	wg.Wait()
	ic.mu.Lock()
	log.Printf("Icons: %d loaded, %d failed", ic.done, ic.failed)
	ic.mu.Unlock()
}

func (ic *Icons) download(t iconTask) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		rel, err := ic.downloadOnce(t)
		if err == nil {
			return rel, nil
		}
		lastErr = err
		time.Sleep(400 * time.Millisecond)
	}
	return "", lastErr
}

func (ic *Icons) downloadOnce(t iconTask) (string, error) {
	resp, err := ic.request(t.url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "image/") && ct != "application/octet-stream" {
		return "", fmt.Errorf("not an image (%s)", ct)
	}
	u, _ := url.Parse(t.url)
	ext := strings.ToLower(path.Ext(u.Path))
	if !imgExts[ext] {
		ext = ".webp"
	}
	rel := t.kind + "/" + t.key + ext
	full := filepath.Join(ic.dir, filepath.FromSlash(rel))
	tmp := full + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, 3<<20))
	f.Close()
	if err != nil || n == 0 {
		os.Remove(tmp)
		if err == nil {
			err = fmt.Errorf("empty file")
		}
		return "", err
	}
	if err := os.Rename(tmp, full); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return rel, nil
}
