package template

import "testing"

// Dwarf's "Change Speed to 20 feet if higher": replacing the numeric value
// must sync the sibling display name ("25 feet" → "20 feet").
func TestReplaceSyncsSiblingText(t *testing.T) {
	sb := map[string]any{
		"offense": map[string]any{
			"speed": map[string]any{
				"movement": []any{
					map[string]any{"movement_type": "walk", "name": "25 feet",
						"subtype": "speed", "type": "stat_block_section", "value": float64(25)},
				},
			},
		},
	}
	eff := Effect{
		Operation:   "replace",
		Target:      "$.offense.speed.movement[?(@.movement_type=='walk')].value",
		Value:       float64(20),
		Conditional: "$.offense.speed.movement[?(@.movement_type=='walk')].value > 20",
	}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
	m := sb["offense"].(map[string]any)["speed"].(map[string]any)["movement"].([]any)[0].(map[string]any)
	if m["value"] != float64(20) || m["name"] != "20 feet" {
		t.Errorf("expected value 20 / name '20 feet', got %v / %v", m["value"], m["name"])
	}
}

// Replacing non-numeric values (spell swaps replace whole objects) must not
// attempt text sync.
func TestReplaceNonNumericUntouched(t *testing.T) {
	sb := map[string]any{
		"offense": map[string]any{
			"speed": map[string]any{
				"movement": []any{
					map[string]any{"movement_type": "walk", "name": "25 feet", "value": float64(25)},
				},
			},
		},
	}
	eff := Effect{
		Operation: "replace",
		Target:    "$.offense.speed.movement[?(@.movement_type=='walk')].name",
		Value:     "hover 25 feet",
	}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
	m := sb["offense"].(map[string]any)["speed"].(map[string]any)["movement"].([]any)[0].(map[string]any)
	if m["name"] != "hover 25 feet" || m["value"] != float64(25) {
		t.Errorf("unexpected mutation: %v", m)
	}
}
