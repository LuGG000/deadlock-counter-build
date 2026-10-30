package main

import "testing"

func TestUnreachableHero(t *testing.T) {
	d, err := loadData()
	if err != nil {
		t.Fatal(err)
	}
	hero := func(name string) *Hero {
		for _, h := range d.Heroes {
			if h.Name == name {
				return h
			}
		}
		t.Fatalf("hero %s missing", name)
		return nil
	}
	cc := []*Hero{}
	for _, h := range d.Heroes {
		if h.isCC && h.Name != "Vindicta" && len(cc) < 4 {
			cc = append(cc, h)
		}
	}
	vin, other := hero("Vindicta"), hero("Haze")
	vv, _ := computeVotes(vin, cc, nil)
	ov, _ := computeVotes(other, cc, nil)
	for _, n := range ccDefense {
		if vv.score[nk(n)] != 0 {
			t.Errorf("Vindicta should not value %s against CC, got %v", n, vv.score[nk(n)])
		}
	}
	if ov.score[nk("Debuff Reducer")] <= 0 {
		t.Error("a grounded hero should still value Debuff Reducer against 4 CC heroes")
	}
	ev, _ := computeVotes(other, []*Hero{vin}, nil)
	if ev.score[nk("Slowing Hex")] < 2 {
		t.Errorf("enemy Vindicta should add Slowing Hex, got %v", ev.score[nk("Slowing Hex")])
	}
}

// Every hero needs a build; all items must exist in the item DB and be purchasable.
func TestAllHeroesComplete(t *testing.T) {
	d, err := loadData()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Heroes) < 44 {
		t.Errorf("only %d heroes", len(d.Heroes))
	}
	for _, h := range d.Heroes {
		if !h.HasBuild || len(h.Catalog.Core) < 8 {
			t.Errorf("%s: no usable build (%d core)", h.Name, len(h.Catalog.Core))
		}
		if len(h.counter) == 0 {
			t.Errorf("%s: no counter items", h.Name)
		}
		if _, ok := heroIDs[norm(h.Name)]; !ok {
			t.Errorf("%s: no hero ID", h.Name)
		}
		if _, ok := heroAbil[norm(h.Name)]; !ok {
			t.Errorf("%s: no ability IDs", h.Name)
		}
		all := append(append(append([]string{}, h.Catalog.Early...), h.Catalog.Core...), h.Catalog.Late...)
		all = append(all, h.counter...)
		for _, n := range all {
			it := d.ItemMap[norm(n)]
			if it == nil {
				t.Errorf("%s: item %q not in the item DB", h.Name, n)
				continue
			}
			if it.IName == "" {
				t.Errorf("%s: item %q has no internal_name (not exportable)", h.Name, n)
			}
		}
		// A calculation against a sample team must not warn
		var en []*Hero
		for _, o := range d.Heroes {
			if o != h && len(en) < 6 {
				en = append(en, o)
			}
		}
		r := d.recommend(h, en, nil, h.Catalog, 3)
		if len(r.Final) == 0 || len(r.Final) > 12 {
			t.Errorf("%s: final build has %d items", h.Name, len(r.Final))
		}
		for _, w := range r.Warnings {
			t.Errorf("%s: warning %s", h.Name, w)
		}
	}
}
