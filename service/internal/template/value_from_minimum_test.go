package template

import "testing"

// Amphibious on a fly-only creature: "add a swim Speed equal to half its
// land Speed (minimum 15 feet)" — no land speed exists, so the published
// minimum floors the grant instead of skipping it.
func TestValueFromMissingSourceUsesMinimum(t *testing.T) {
	sb := map[string]any{
		"offense": map[string]any{
			"speed": map[string]any{
				"movement": []any{
					map[string]any{"movement_type": "fly", "name": "fly 40 feet",
						"subtype": "speed", "type": "stat_block_section", "value": float64(40)},
				},
			},
		},
	}
	min := 15.0
	effs := []Effect{
		{
			Operation: "add_item",
			Target:    "$.offense.speed.movement",
			Item: map[string]any{"movement_type": "swim", "name": "swim",
				"subtype": "speed", "type": "stat_block_section"},
			ValueFrom: "$.offense.speed.movement[?(@.movement_type=='walk')].value / 2",
			Minimum:   &min,
		},
		{
			Operation: "add_item",
			Target:    "$.offense.speed.movement",
			Item: map[string]any{"movement_type": "walk", "name": "walk",
				"subtype": "speed", "type": "stat_block_section"},
			ValueFrom: "$.offense.speed.movement[?(@.movement_type=='swim')].value / 2",
			Minimum:   &min,
		},
	}
	for _, e := range effs {
		if err := applyEffectGroup(sb, []Effect{e}); err != nil {
			t.Fatalf("applyEffectGroup: %v", err)
		}
	}
	mv := sb["offense"].(map[string]any)["speed"].(map[string]any)["movement"].([]any)
	got := map[string]float64{}
	for _, m := range mv {
		mm := m.(map[string]any)
		if v, ok := toFloat64(mm["value"]); ok {
			got[mm["movement_type"].(string)] = v
		}
	}
	if got["swim"] != 15 || got["walk"] != 15 || got["fly"] != 40 {
		t.Errorf("expected swim 15, walk 15, fly 40; got %v", got)
	}
}

// Without a minimum, a missing source still skips the grant.
func TestValueFromMissingSourceNoMinimumSkips(t *testing.T) {
	sb := map[string]any{
		"offense": map[string]any{
			"speed": map[string]any{"movement": []any{}},
		},
	}
	eff := Effect{
		Operation: "add_item",
		Target:    "$.offense.speed.movement",
		Item: map[string]any{"movement_type": "swim", "name": "swim",
			"subtype": "speed", "type": "stat_block_section"},
		ValueFrom: "$.offense.speed.movement[?(@.movement_type=='walk')].value / 2",
	}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
	mv := sb["offense"].(map[string]any)["speed"].(map[string]any)["movement"].([]any)
	if len(mv) != 0 {
		t.Errorf("expected no grant without minimum, got %v", mv)
	}
}

// Single-shot: the walk grant alone against a fly-only creature exercises
// the missing-source branch directly (no just-added swim to divide), and
// checks setMovementName's bare walk naming.
func TestValueFromMissingSourceWalkSingleShot(t *testing.T) {
	sb := map[string]any{
		"offense": map[string]any{
			"speed": map[string]any{
				"movement": []any{
					map[string]any{"movement_type": "fly", "name": "fly 40 feet",
						"subtype": "speed", "type": "stat_block_section", "value": float64(40)},
				},
			},
		},
	}
	min := 15.0
	eff := Effect{
		Operation: "add_item",
		Target:    "$.offense.speed.movement",
		Item: map[string]any{"movement_type": "walk", "name": "walk",
			"subtype": "speed", "type": "stat_block_section"},
		ValueFrom: "$.offense.speed.movement[?(@.movement_type=='swim')].value / 2",
		Minimum:   &min,
	}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
	mv := sb["offense"].(map[string]any)["speed"].(map[string]any)["movement"].([]any)
	last := mv[len(mv)-1].(map[string]any)
	if last["movement_type"] != "walk" || last["value"] != 15.0 || last["name"] != "15 feet" {
		t.Errorf("walk grant wrong: %v", last)
	}
}
