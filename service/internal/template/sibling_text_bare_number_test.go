package template

import "testing"

// A Striking rune replaces a strike's dice_count; the sibling formula must re-render
// ("1d6" -> "2d6"), but a bare-numeric sibling like a weapon mode's hands:"1" is a
// value, not display text, and must NOT be dragged from "1" to "2" (regression:
// Striking was turning a 1-handed rapier 2-handed).
func TestSiblingTextSync_SkipsBareNumberField(t *testing.T) {
	item := map[string]any{"stat_block": map[string]any{"offense": map[string]any{
		"weapon_modes": []any{map[string]any{
			"weapon_type": "Melee",
			"hands":       "1", // real data stores hands as a string
			"damage": []any{map[string]any{
				"damage_type": "piercing", "dice_count": float64(1), "die_size": float64(6), "formula": "1d6",
			}},
		}},
	}}}
	tmpl := TemplateJSON{Name: "Striking", MonsterTemplate: MonsterTemplate{Changes: []Change{{
		Text: "Striking", ChangeCategory: "rune",
		Effects: []Effect{{Operation: "replace", Target: "$.offense.weapon_modes[*].damage[*].dice_count", Value: float64(2)}},
	}}}}

	resp, err := Apply(item, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	mode := resp.Creature["stat_block"].(map[string]any)["offense"].(map[string]any)["weapon_modes"].([]any)[0].(map[string]any)
	if hands := mode["hands"]; hands != "1" {
		t.Errorf("hands corrupted by sibling-text sync: got %v, want unchanged \"1\"", hands)
	}
	dmg := mode["damage"].([]any)[0].(map[string]any)
	if dmg["formula"] != "2d6" {
		t.Errorf("formula not re-rendered: got %v, want \"2d6\"", dmg["formula"])
	}
	if dmg["dice_count"] != float64(2) {
		t.Errorf("dice_count = %v, want 2", dmg["dice_count"])
	}
}

// The bare-number skip must not regress the monster-template case it was built for:
// a numeric replace still syncs display text that carries a unit/label.
func TestSiblingTextSync_StillSyncsDisplayText(t *testing.T) {
	sb := map[string]any{"stat_block": map[string]any{"speeds": []any{
		map[string]any{"movement_type": "walk", "value": float64(25), "name": "25 feet"},
	}}}
	tmpl := TemplateJSON{Name: "Slow", MonsterTemplate: MonsterTemplate{Changes: []Change{{
		Text: "Slow", ChangeCategory: "template",
		Effects: []Effect{{Operation: "replace", Target: "$.speeds[*].value", Value: float64(20)}},
	}}}}
	resp, err := Apply(sb, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	sp := resp.Creature["stat_block"].(map[string]any)["speeds"].([]any)[0].(map[string]any)
	if sp["name"] != "20 feet" {
		t.Errorf("display text not synced: got %v, want \"20 feet\"", sp["name"])
	}
}
