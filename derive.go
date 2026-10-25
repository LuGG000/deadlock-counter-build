package main

// Abgeleitete Daten fuer Helden ohne Tracklock-Build (zu neu / zu wenig Matches):
// Build und Gegenitems werden aus aehnlichen Helden (gleiche Rolle, aehnliche Schadensverteilung) zusammengesetzt.
// Sobald echte Daten im Katalog stehen, greift das automatisch nicht mehr.

import (
	"sort"
)

func l1(a, b [3]int) int {
	t := 0
	for i := 0; i < 3; i++ {
		x := a[i] - b[i]
		if x < 0 {
			x = -x
		}
		t += x
	}
	return t
}

// pickTop: Items nach Haeufigkeit (gewichtet nach Position) auswaehlen, dann nach Durchschnittsposition ordnen.
func pickTop(lists [][]string, n int, d *Data) []string {
	type agg struct {
		name  string
		score float64
		pos   float64
		cnt   int
	}
	m := map[string]*agg{}
	for _, l := range lists {
		for i, name := range l {
			it := d.ItemMap[norm(name)]
			if it == nil {
				continue
			}
			a := m[it.key]
			if a == nil {
				a = &agg{name: it.Name}
				m[it.key] = a
			}
			rel := float64(i) / float64(len(l))
			a.score += 1.0 + (1-rel)*0.2
			a.pos += rel
			a.cnt++
		}
	}
	all := make([]*agg, 0, len(m))
	for _, a := range m {
		all = append(all, a)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].name < all[j].name
	})
	if len(all) > n {
		all = all[:n]
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].pos/float64(all[i].cnt) < all[j].pos/float64(all[j].cnt) })
	out := make([]string, len(all))
	for i, a := range all {
		out[i] = a.name
	}
	return out
}

func deriveMissing(d *Data) []string {
	var done []string
	for _, h := range d.Heroes {
		needBuild := len(h.Catalog.Core) == 0
		needCounter := len(h.counter) == 0
		if !needBuild && !needCounter {
			continue
		}
		var cands []*Hero
		for _, o := range d.Heroes {
			if o == h || len(o.Catalog.Core) == 0 || o.Derived {
				continue
			}
			if o.Role == h.Role {
				cands = append(cands, o)
			}
		}
		if len(cands) < 3 {
			cands = cands[:0]
			for _, o := range d.Heroes {
				if o != h && len(o.Catalog.Core) > 0 && !o.Derived {
					cands = append(cands, o)
				}
			}
		}
		sort.SliceStable(cands, func(i, j int) bool { return l1(cands[i].Shares, h.Shares) < l1(cands[j].Shares, h.Shares) })
		if len(cands) > 5 {
			cands = cands[:5]
		}
		if needBuild {
			var cores, earlies, lates [][]string
			for _, o := range cands {
				cores = append(cores, o.Catalog.Core)
				earlies = append(earlies, o.Catalog.Early)
				lates = append(lates, o.Catalog.Late)
			}
			h.Catalog.Core = pickTop(cores, 14, d)
			h.Catalog.Early = pickTop(earlies, 8, d)
			h.Catalog.Late = pickTop(lates, 5, d)
			h.HasBuild, h.Derived = true, true
		}
		if needCounter {
			// Gegenitems: alle Helden der gleichen Rolle, haeufigste zuerst
			var cl [][]string
			for _, o := range d.Heroes {
				if o != h && o.Role == h.Role && len(o.counter) > 0 && !o.Derived {
					cl = append(cl, o.counter)
				}
			}
			h.counter = pickTop(cl, 8, d)
			h.cKeys = h.cKeys[:0]
			for _, c := range h.counter {
				h.cKeys = append(h.cKeys, norm(c))
			}
			h.DerivedCounter = true
		}
		var names []string
		for _, o := range cands {
			names = append(names, o.Name)
		}
		done = append(done, h.Name+" ← "+join(names, ", "))
	}
	return done
}

func join(s []string, sep string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += sep
		}
		out += x
	}
	return out
}
