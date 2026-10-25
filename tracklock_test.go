package main

import (
	"fmt"
	"strings"
	"testing"
)

func img(cat, slug, alt string) string {
	return fmt.Sprintf(`<div class="x"><img alt="%s" src="https://static.bigbrain.gg/deadlock-img/assets-api-res/images/items/%s/%s.webp" loading="lazy"/><span>I</span></div>`, alt, cat, slug)
}

func TestParseTracklock(t *testing.T) {
	d, err := loadData()
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	sb.WriteString(`<nav><a>Builds</a><a>Items</a></nav><p>62.0% win rate across 955 matches</p><h2>Core</h2>`)
	coreNames := []string{"Monster Rounds", "High-Velocity Rounds", "Sprint Boots", "Opening Rounds", "Rapid Rounds", "Swift Striker", "Titanic Magazine", "Enduring Speed"}
	for _, n := range coreNames {
		sb.WriteString(img("weapon", strings.ToLower(strings.ReplaceAll(n, " ", "_")), n))
		sb.WriteString(img("weapon", strings.ToLower(strings.ReplaceAll(n, " ", "_")), n)) // duplicate
	}
	sb.WriteString(`<h2>Early</h2>` + img("weapon", "extended_magazine", "Extended Magazine") + `<h2>Late</h2>` + img("spirit", "superior_cooldown", "Superior Cooldown"))
	sb.WriteString(`<h2>Counter Picks</h2>` + img("vitality", "unstoppable", "Unstoppable") + `<h2>Actives</h2>` + img("vitality", "metal_skin", "Metal Skin"))
	b, unres := parseTracklockHTML(d, sb.String())
	if len(unres) != 0 {
		t.Errorf("unresolved: %v", unres)
	}
	if strings.Join(b.Core, ",") != strings.Join(coreNames, ",") {
		t.Errorf("core: %v", b.Core)
	}
	if len(b.Early) != 1 || len(b.Late) != 1 {
		t.Errorf("early/late: %v %v", b.Early, b.Late)
	}
	if b.WR != 62 {
		t.Errorf("WR %v", b.WR)
	}
	if !tlValid(b) {
		t.Error("not valid")
	}
	enc := `<h2>Core</h2><img src="/_next/image?url=https%3A%2F%2Fstatic.bigbrain.gg%2Fdeadlock-img%2Fassets-api-res%2Fimages%2Fitems%2Fweapon%2Frapid_rounds.webp&w=64&q=75" alt="">`
	if b3, _ := parseTracklockHTML(d, enc); len(b3.Core) != 1 {
		t.Errorf("encoded: %v", b3.Core)
	}
	// Without alt text, only via the image slug
	b2, _ := parseTracklockHTML(d, `<h2>Core</h2>`+strings.Repeat(img("weapon", "rapid_rounds", ""), 1))
	if len(b2.Core) != 1 {
		t.Errorf("slug-only: %v", b2.Core)
	}
	for _, n := range []string{"Mo & Krill", "The Doorman", "Rat King", "Deadman Danny"} {
		t.Log(n, heroSlugs(n))
	}
}
