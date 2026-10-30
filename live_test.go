package main

import "testing"

func TestLiveTip(t *testing.T) {
	o := map[string]any{
		"properties": map[string]any{
			"BonusBulletSpeedPercent":      map[string]any{"value": "45", "postfix": "%", "label": "Bullet Velocity"},
			"AbilityCastRange":             map[string]any{"value": "40m", "postfix": "m", "label": "Cast Range"},
			"AbilityCooldownBetweenCharge": map[string]any{"value": "-1.0", "postfix": "s", "label": "Charge Delay"},
			"AbilityChannelTime":           map[string]any{"value": "0", "postfix": "s", "label": "Channel Time"},
			"HealAmpReceivePenaltyPercent": map[string]any{"value": "-40", "postfix": "%", "label": "Incoming Healing"},
			"HealAmpRegenPenaltyPercent":   map[string]any{"value": "-40", "postfix": "%", "label": "Incoming Healing"},
			"TickRate":                     map[string]any{"value": "0.5"},
		},
		"description": map[string]any{"desc": `Your <span class="highlight">bullets</span> &amp; more <svg width="1"><path d="M0"/></svg>stuff.`},
	}
	want := "Cast Range 40m, Bullet Velocity 45%, Incoming Healing -40% | Your bullets & more stuff."
	if got := liveTip(o); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if got := liveTip(map[string]any{}); got != "" {
		t.Errorf("empty item: %q", got)
	}
}
