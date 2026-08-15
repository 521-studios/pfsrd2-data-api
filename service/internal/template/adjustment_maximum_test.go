package template

import "testing"

// Reinforcing rune: "Hardness increases by 3" but capped at the grade's maximum.
// The adjustment adds the delta and clamps the result down to `maximum`, syncing
// the sibling display text to the clamped value. Mirrors the Minimum handling.
func TestAdjustmentMaximumClampsAndSyncsText(t *testing.T) {
	max8 := 8.0
	cases := []struct {
		name     string
		start    float64
		startTxt string
		maximum  *float64
		want     float64
		wantTxt  string
	}{
		{"clamped to max", 6, "Hardness 6", &max8, 8, "Hardness 8"},
		{"under the cap, no clamp", 3, "Hardness 3", &max8, 6, "Hardness 6"},
		{"no maximum set, unchanged behavior", 6, "Hardness 6", nil, 9, "Hardness 9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sb := map[string]any{
				"defense": map[string]any{
					"hitpoints": map[string]any{
						"hardness": map[string]any{
							"type": "stat_block_section", "subtype": "hardness",
							"value": tc.start, "text": tc.startTxt,
						},
					},
				},
			}
			eff := Effect{
				Operation: "adjustment",
				Target:    "$.defense.hitpoints.hardness.value",
				Value:     float64(3),
				Maximum:   tc.maximum,
			}
			if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
				t.Fatalf("applyEffectGroup: %v", err)
			}
			h := sb["defense"].(map[string]any)["hitpoints"].(map[string]any)["hardness"].(map[string]any)
			if got, _ := toFloat64(h["value"]); got != tc.want {
				t.Errorf("value = %v, want %v", got, tc.want)
			}
			if h["text"] != tc.wantTxt {
				t.Errorf("text = %q, want %q", h["text"], tc.wantTxt)
			}
		})
	}
}

// The array-element adjustment path honours Maximum per element (a max on an array
// adjustment must clamp, not be silently ignored).
func TestAdjustmentMaximumClampsArrayElements(t *testing.T) {
	max8 := 8.0
	sb := map[string]any{
		"offense": map[string]any{"attacks": map[string]any{"bonuses": []any{7.0, 4.0, 1.0}}},
	}
	eff := Effect{Operation: "adjustment", Target: "$.offense.attacks.bonuses",
		Value: float64(3), Maximum: &max8}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
	got := sb["offense"].(map[string]any)["attacks"].(map[string]any)["bonuses"].([]any)
	want := []float64{8, 7, 4} // 7+3→10 clamped to 8; 4+3=7; 1+3=4
	for i, w := range want {
		if g, _ := toFloat64(got[i]); g != w {
			t.Errorf("bonuses[%d] = %v, want %v", i, got[i], w)
		}
	}
}
