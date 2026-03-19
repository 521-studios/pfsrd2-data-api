package template

import (
	"encoding/json"
	"testing"
)

func TestResolvePath_DirectNested(t *testing.T) {
	sb := map[string]any{
		"defense": map[string]any{
			"ac": map[string]any{
				"value": float64(37),
			},
		},
	}
	results, err := resolvePath(sb, "$.defense.ac.value")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Get() != float64(37) {
		t.Errorf("expected 37, got %v", results[0].Get())
	}
}

func TestResolvePath_WildcardArray(t *testing.T) {
	sb := map[string]any{
		"skills": []any{
			map[string]any{"name": "Acrobatics", "value": float64(23)},
			map[string]any{"name": "Arcana", "value": float64(25)},
		},
	}
	results, err := resolvePath(sb, "$.skills[*].value")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Get() != float64(23) {
		t.Errorf("expected 23, got %v", results[0].Get())
	}
	if results[1].Get() != float64(25) {
		t.Errorf("expected 25, got %v", results[1].Get())
	}
}

func TestResolvePath_BonusesArray(t *testing.T) {
	sb := map[string]any{
		"offense": map[string]any{
			"offensive_actions": []any{
				map[string]any{
					"attack": map[string]any{
						"bonus": map[string]any{
							"bonuses": []any{float64(29), float64(24), float64(19)},
						},
					},
				},
			},
		},
	}
	results, err := resolvePath(sb, "$.offense.offensive_actions[*].attack.bonus.bonuses")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result (the bonuses array itself), got %d", len(results))
	}
	arr, ok := results[0].Get().([]any)
	if !ok {
		t.Fatal("expected []any")
	}
	if len(arr) != 3 {
		t.Errorf("expected 3 bonuses, got %d", len(arr))
	}
}

func TestResolvePath_MissingField(t *testing.T) {
	sb := map[string]any{}
	results, err := resolvePath(sb, "$.nonexistent.field")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for missing path, got %d", len(results))
	}
}

func TestResolvePath_WildcardElementsDirectly(t *testing.T) {
	sb := map[string]any{
		"defense": map[string]any{
			"hitpoints": []any{
				map[string]any{"hp": float64(305)},
			},
		},
	}
	results, err := resolvePath(sb, "$.defense.hitpoints[*].hp")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Get() != float64(305) {
		t.Errorf("expected 305, got %v", results[0].Get())
	}
}

func TestResolvePath_NestedWildcard(t *testing.T) {
	// $.offense.offensive_actions[*].attack.damage resolves to the damage array
	sb := map[string]any{
		"offense": map[string]any{
			"offensive_actions": []any{
				map[string]any{
					"attack": map[string]any{
						"damage": []any{
							map[string]any{"formula": "3d12+15"},
						},
					},
				},
				map[string]any{
					"offensive_action_type": "ability",
					"ability":               map[string]any{"name": "Breath Weapon"},
				},
			},
		},
	}
	results, err := resolvePath(sb, "$.offense.offensive_actions[*].attack.damage")
	if err != nil {
		t.Fatal(err)
	}
	// Only first offensive_action has attack.damage
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestSplitPath(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"defense.ac.value", []string{"defense", "ac", "value"}},
		{"skills[*].value", []string{"skills", "[*]", "value"}},
		{"offense.offensive_actions[*].attack.bonus.bonuses", []string{"offense", "offensive_actions", "[*]", "attack", "bonus", "bonuses"}},
		{"defense.hitpoints[*].hp", []string{"defense", "hitpoints", "[*]", "hp"}},
	}
	for _, tt := range tests {
		got := splitPath(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitPath(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitPath(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestToJSONPointer(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"$.defense.ac.value", "/stat_block/defense/ac/value"},
		{"$.creature_type.level", "/stat_block/creature_type/level"},
		{"$.skills[*].value", "/stat_block/skills/value"},
	}
	for _, tt := range tests {
		got := toJSONPointer(tt.input)
		if got != tt.want {
			t.Errorf("toJSONPointer(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestResolvedValue_Set(t *testing.T) {
	sb := map[string]any{
		"defense": map[string]any{
			"ac": map[string]any{
				"value": float64(37),
			},
		},
	}
	results, _ := resolvePath(sb, "$.defense.ac.value")
	results[0].Set(float64(39))

	// Verify mutation
	raw, _ := json.Marshal(sb)
	var check map[string]any
	json.Unmarshal(raw, &check)
	ac := check["defense"].(map[string]any)["ac"].(map[string]any)["value"].(float64)
	if ac != 39 {
		t.Errorf("expected 39 after Set, got %v", ac)
	}
}
