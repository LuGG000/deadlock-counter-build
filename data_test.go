package main

import "testing"

// Jeder Held braucht ein Build, alle Items muessen in der Item-DB existieren und kaufbar sein.
func TestAllHeroesComplete(t *testing.T) {
	d, err := loadData()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Heroes) < 44 {
		t.Errorf("nur %d Helden", len(d.Heroes))
	}
	for _, h := range d.Heroes {
		if !h.HasBuild || len(h.Catalog.Core) < 8 {
			t.Errorf("%s: kein brauchbares Build (%d Core)", h.Name, len(h.Catalog.Core))
		}
		if len(h.counter) == 0 {
			t.Errorf("%s: keine Gegenitems", h.Name)
		}
		if _, ok := heroIDs[norm(h.Name)]; !ok {
			t.Errorf("%s: keine Hero-ID", h.Name)
		}
		if _, ok := heroAbil[norm(h.Name)]; !ok {
			t.Errorf("%s: keine Faehigkeiten-IDs", h.Name)
		}
		all := append(append(append([]string{}, h.Catalog.Early...), h.Catalog.Core...), h.Catalog.Late...)
		all = append(all, h.counter...)
		for _, n := range all {
			it := d.ItemMap[norm(n)]
			if it == nil {
				t.Errorf("%s: Item %q nicht in der Item-DB", h.Name, n)
				continue
			}
			if it.IName == "" {
				t.Errorf("%s: Item %q ohne internal_name (nicht exportierbar)", h.Name, n)
			}
		}
		// Berechnung gegen ein Beispielteam darf nicht warnen
		var en []*Hero
		for _, o := range d.Heroes {
			if o != h && len(en) < 6 {
				en = append(en, o)
			}
		}
		r := d.recommend(h, en, nil, h.Catalog, 3)
		if len(r.Final) == 0 || len(r.Final) > 12 {
			t.Errorf("%s: Endbuild %d Items", h.Name, len(r.Final))
		}
		for _, w := range r.Warnings {
			t.Errorf("%s: Warnung %s", h.Name, w)
		}
	}
}
