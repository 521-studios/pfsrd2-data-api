package template

import (
	"encoding/json"
	"reflect"
	"testing"
)

// helper: build a damage array from compact specs
func dmg(entries ...map[string]any) []any {
	arr := make([]any, len(entries))
	for i, e := range entries {
		full := map[string]any{"subtype": "attack_damage", "type": "stat_block_section"}
		for k, v := range e {
			full[k] = v
		}
		arr[i] = full
	}
	return arr
}

func applyROD(t *testing.T, damage []any) []any {
	t.Helper()
	sb := map[string]any{
		"offense": map[string]any{
			"offensive_actions": []any{
				map[string]any{
					"name":   "Melee",
					"attack": map[string]any{"damage": damage},
				},
			},
		},
	}
	eff := Effect{
		Operation: "replace_one_die",
		Target:    "$.offense.offensive_actions[*].attack.damage",
		Value:     "fire",
	}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
	oa := sb["offense"].(map[string]any)["offensive_actions"].([]any)
	return oa[0].(map[string]any)["attack"].(map[string]any)["damage"].([]any)
}

func compact(arr []any) []map[string]any {
	out := make([]map[string]any, len(arr))
	for i, e := range arr {
		m := e.(map[string]any)
		c := map[string]any{}
		for _, k := range []string{"formula", "damage_type", "persistent", "effect"} {
			if v, ok := m[k]; ok {
				c[k] = v
			}
		}
		out[i] = c
	}
	return out
}

func TestReplaceOneDie_SplitsMultiDieEntry(t *testing.T) {
	// 2d8+9 piercing + 1d6 cold → 1d8+9 piercing + 1d8 fire + 1d6 cold
	got := compact(applyROD(t, dmg(
		map[string]any{"formula": "2d8+9", "damage_type": "piercing"},
		map[string]any{"formula": "1d6", "damage_type": "cold"},
	)))
	want := []map[string]any{
		{"formula": "1d8+9", "damage_type": "piercing"},
		{"formula": "1d8", "damage_type": "fire"},
		{"formula": "1d6", "damage_type": "cold"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestReplaceOneDie_SingleDieAddsFlatOne(t *testing.T) {
	// 1d6+1 piercing → 1d6+1 piercing + 1 fire
	got := compact(applyROD(t, dmg(
		map[string]any{"formula": "1d6+1", "damage_type": "piercing"},
	)))
	want := []map[string]any{
		{"formula": "1d6+1", "damage_type": "piercing"},
		{"formula": "1", "damage_type": "fire"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestReplaceOneDie_ConvertsLastSingleDieEntry(t *testing.T) {
	// 1d8 piercing + 1d6 cold (two dice, none splittable) → 1d8 piercing + 1d6 fire
	got := compact(applyROD(t, dmg(
		map[string]any{"formula": "1d8", "damage_type": "piercing"},
		map[string]any{"formula": "1d6", "damage_type": "cold"},
	)))
	want := []map[string]any{
		{"formula": "1d8", "damage_type": "piercing"},
		{"formula": "1d6", "damage_type": "fire"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestReplaceOneDie_AlreadyFireUnchanged(t *testing.T) {
	before := dmg(map[string]any{"formula": "2d6", "damage_type": "fire"})
	got := compact(applyROD(t, before))
	want := []map[string]any{{"formula": "2d6", "damage_type": "fire"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestReplaceOneDie_SkipsPersistentRider(t *testing.T) {
	// 4d12+12 piercing + 1d8 persistent fire → 3d12+12 piercing + 1d12 fire + rider
	got := compact(applyROD(t, dmg(
		map[string]any{"formula": "4d12+12", "damage_type": "piercing"},
		map[string]any{"formula": "1d8", "damage_type": "fire", "persistent": true},
	)))
	want := []map[string]any{
		{"formula": "3d12+12", "damage_type": "piercing"},
		{"formula": "1d12", "damage_type": "fire"},
		{"formula": "1d8", "damage_type": "fire", "persistent": true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestReplaceOneDie_EffectOnlyUnchanged(t *testing.T) {
	before := dmg(map[string]any{"effect": "web"})
	got := compact(applyROD(t, before))
	want := []map[string]any{{"effect": "web"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestReplaceOneDie_NewEntryShape(t *testing.T) {
	got := applyROD(t, dmg(map[string]any{"formula": "2d8", "damage_type": "slashing"}))
	added := got[1].(map[string]any)
	if added["subtype"] != "attack_damage" || added["type"] != "stat_block_section" {
		b, _ := json.Marshal(added)
		t.Errorf("added entry missing standard fields: %s", b)
	}
}
