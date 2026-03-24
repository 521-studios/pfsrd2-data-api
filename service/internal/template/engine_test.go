package template

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fixtureDir returns the path to the pfsrd2-data directory relative to this test file.
func fixtureDir() string {
	_, file, _, _ := runtime.Caller(0)
	// service/internal/template/engine_test.go → walk up to 521Studios
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "pfsrd2-data")
}

func loadCreature(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join(fixtureDir(), "monsters", "bestiary", "adult_red_dragon.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixture not available: %v", err)
	}
	var creature map[string]any
	if err := json.Unmarshal(data, &creature); err != nil {
		t.Fatalf("parse creature: %v", err)
	}
	return creature
}

func loadTemplate(t *testing.T, name string) TemplateJSON {
	t.Helper()
	path := filepath.Join(fixtureDir(), "monster_templates", "monster_core", name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixture not available: %v", err)
	}
	var tmpl TemplateJSON
	if err := json.Unmarshal(data, &tmpl); err != nil {
		t.Fatalf("parse template: %v", err)
	}
	return tmpl
}

func getStatBlockValue(t *testing.T, creature map[string]any, path ...string) any {
	t.Helper()
	current := any(creature["stat_block"])
	for _, key := range path {
		cm, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("expected map at %q, got %T", key, current)
		}
		current = cm[key]
	}
	return current
}

func TestApply_Elite_AdultRedDragon(t *testing.T) {
	creature := loadCreature(t)
	tmpl := loadTemplate(t, "elite")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(resp.PatchDoc.AppliedPatches) == 0 {
		t.Fatal("expected patches, got none")
	}

	// Check that we have patch groups for the expected categories
	cats := map[string]bool{}
	for _, pg := range resp.PatchDoc.AppliedPatches {
		cats[pg.ChangeCategory] = true
		if len(pg.Operations) == 0 {
			t.Errorf("patch group %q has no operations", pg.ChangeCategory)
		}
	}
	for _, want := range []string{"level", "combat_stats", "damage", "hit_points"} {
		if !cats[want] {
			t.Errorf("missing patch group for %q", want)
		}
	}

	// Verify specific values in the result
	level := getStatBlockValue(t, resp.Creature, "creature_type", "level")
	if level != float64(15) {
		t.Errorf("expected level 15, got %v", level)
	}

	ac := getStatBlockValue(t, resp.Creature, "defense", "ac", "value")
	if ac != float64(39) {
		t.Errorf("expected AC 39, got %v", ac)
	}

	fort := getStatBlockValue(t, resp.Creature, "defense", "saves", "fort", "value")
	if fort != float64(30) {
		t.Errorf("expected Fort 30, got %v", fort)
	}

	ref := getStatBlockValue(t, resp.Creature, "defense", "saves", "ref", "value")
	if ref != float64(27) {
		t.Errorf("expected Ref 27, got %v", ref)
	}

	will := getStatBlockValue(t, resp.Creature, "defense", "saves", "will", "value")
	if will != float64(28) {
		t.Errorf("expected Will 28, got %v", will)
	}

	// HP should be 305 + 20 = 325 (level 14 is in 5-19 range)
	hpArr := getStatBlockValue(t, resp.Creature, "defense", "hitpoints")
	hpSlice, ok := hpArr.([]any)
	if !ok || len(hpSlice) == 0 {
		t.Fatal("expected hitpoints array")
	}
	hp := hpSlice[0].(map[string]any)["hp"]
	if hp != float64(325) {
		t.Errorf("expected HP 325, got %v", hp)
	}

	perception := getStatBlockValue(t, resp.Creature, "senses", "perception", "value")
	if perception != float64(28) {
		t.Errorf("expected Perception 28, got %v", perception)
	}

	// Skills should be adjusted +2 (path is now $.statistics.skills[*].value)
	skills := getStatBlockValue(t, resp.Creature, "statistics", "skills")
	if skillArr, ok := skills.([]any); ok && len(skillArr) > 0 {
		first := skillArr[0].(map[string]any)
		// Adult Red Dragon Acrobatics is 23, should be 25
		if first["name"] == "Acrobatics" && first["value"] != float64(25) {
			t.Errorf("expected Acrobatics 25 (23+2), got %v", first["value"])
		}
	}

	if len(resp.PatchDoc.Selections) != 0 {
		t.Errorf("expected empty selections, got %v", resp.PatchDoc.Selections)
	}

	// Verify original creature is not mutated
	origLevel := creature["stat_block"].(map[string]any)["creature_type"].(map[string]any)["level"]
	if origLevel != float64(14) {
		t.Errorf("original creature was mutated: level = %v", origLevel)
	}
}

func TestApply_Weak_AdultRedDragon(t *testing.T) {
	creature := loadCreature(t)
	tmpl := loadTemplate(t, "weak")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	level := getStatBlockValue(t, resp.Creature, "creature_type", "level")
	if level != float64(13) {
		t.Errorf("expected level 13, got %v", level)
	}

	ac := getStatBlockValue(t, resp.Creature, "defense", "ac", "value")
	if ac != float64(35) {
		t.Errorf("expected AC 35, got %v", ac)
	}

	hpArr := getStatBlockValue(t, resp.Creature, "defense", "hitpoints")
	hpSlice := hpArr.([]any)
	hp := hpSlice[0].(map[string]any)["hp"]
	if hp != float64(285) {
		t.Errorf("expected HP 285, got %v", hp)
	}
}

func TestApply_Elite_LevelZero(t *testing.T) {
	creature := map[string]any{
		"stat_block": map[string]any{
			"creature_type": map[string]any{"level": float64(0)},
			"defense": map[string]any{
				"ac": map[string]any{"value": float64(14)},
				"saves": map[string]any{
					"fort": map[string]any{"value": float64(5)},
					"ref":  map[string]any{"value": float64(3)},
					"will": map[string]any{"value": float64(2)},
				},
				"hitpoints": []any{
					map[string]any{"hp": float64(10)},
				},
			},
			"offense": map[string]any{
				"offensive_actions": []any{},
			},
			"senses": map[string]any{
				"perception": map[string]any{"value": float64(3)},
			},
			"statistics": map[string]any{
				"skills": []any{},
			},
		},
	}

	tmpl := TemplateJSON{
		MonsterTemplate: MonsterTemplate{
			Name: "Elite",
			Changes: []Change{
				{
					ChangeCategory: "level",
					Text:           "Increase level",
					Effects: []Effect{
						{Conditional: "$.creature_type.level <= 0", Target: "$.creature_type.level", Operation: "adjustment", Value: float64(2)},
						{Conditional: "default", Target: "$.creature_type.level", Operation: "adjustment", Value: float64(1)},
					},
				},
			},
		},
	}

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	level := getStatBlockValue(t, resp.Creature, "creature_type", "level")
	if level != float64(2) {
		t.Errorf("expected level 2 (0 + 2), got %v", level)
	}
}

func TestApply_PatchOperationsAreRFC6902(t *testing.T) {
	creature := loadCreature(t)
	tmpl := loadTemplate(t, "elite")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for _, pg := range resp.PatchDoc.AppliedPatches {
		for _, op := range pg.Operations {
			switch op.Op {
			case "add", "remove", "replace", "move", "copy", "test":
				// valid RFC 6902 op
			default:
				t.Errorf("invalid RFC 6902 op: %q in group %q", op.Op, pg.ChangeCategory)
			}
			if op.Path == "" {
				t.Errorf("empty path in group %q", pg.ChangeCategory)
			}
			if op.Path[0] != '/' {
				t.Errorf("path should start with /: %q", op.Path)
			}
		}
	}
}

func TestApply_BothPartsSerializeToJSON(t *testing.T) {
	creature := loadCreature(t)
	tmpl := loadTemplate(t, "elite")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// PatchDoc round-trips
	patchBytes, err := json.Marshal(resp.PatchDoc)
	if err != nil {
		t.Fatalf("marshal patch doc: %v", err)
	}
	var checkPatch PatchDocument
	if err := json.Unmarshal(patchBytes, &checkPatch); err != nil {
		t.Fatalf("unmarshal patch doc: %v", err)
	}
	if len(checkPatch.AppliedPatches) != len(resp.PatchDoc.AppliedPatches) {
		t.Errorf("round-trip lost patches: %d → %d", len(resp.PatchDoc.AppliedPatches), len(checkPatch.AppliedPatches))
	}

	// Creature round-trips
	creatureBytes, err := json.Marshal(resp.Creature)
	if err != nil {
		t.Fatalf("marshal creature: %v", err)
	}
	var checkCreature map[string]any
	if err := json.Unmarshal(creatureBytes, &checkCreature); err != nil {
		t.Fatalf("unmarshal creature: %v", err)
	}
	if checkCreature["stat_block"] == nil {
		t.Error("creature lost stat_block in round-trip")
	}
}

func TestApply_Chaining_EliteThenWeak(t *testing.T) {
	creature := loadCreature(t)
	elite := loadTemplate(t, "elite")
	weak := loadTemplate(t, "weak")

	// Apply Elite first
	eliteResp, err := Apply(creature, elite)
	if err != nil {
		t.Fatalf("Apply Elite: %v", err)
	}

	// Chain: apply Weak to the Elite result
	weakResp, err := Apply(eliteResp.Creature, weak)
	if err != nil {
		t.Fatalf("Apply Weak: %v", err)
	}

	// Level: 14 → 15 (elite) → 14 (weak -1)
	level := getStatBlockValue(t, weakResp.Creature, "creature_type", "level")
	if level != float64(14) {
		t.Errorf("expected level 14 after elite+weak, got %v", level)
	}

	// AC: 37 → 39 (elite) → 37 (weak -2)
	ac := getStatBlockValue(t, weakResp.Creature, "defense", "ac", "value")
	if ac != float64(37) {
		t.Errorf("expected AC 37 after elite+weak, got %v", ac)
	}
}

func TestApply_AddModifier_AppendsToArray(t *testing.T) {
	creature := map[string]any{
		"stat_block": map[string]any{
			"offense": map[string]any{
				"offensive_actions": []any{
					map[string]any{
						"attack": map[string]any{
							"damage": []any{
								map[string]any{"formula": "1d8+4", "damage_type": "slashing"},
							},
						},
					},
				},
			},
		},
	}

	tmpl := TemplateJSON{
		MonsterTemplate: MonsterTemplate{
			Name: "Test",
			Changes: []Change{
				{
					ChangeCategory: "damage",
					Text:           "Add damage modifier",
					Effects: []Effect{
						{
							Conditional: "default",
							Target:      "$.offense.offensive_actions[*].attack.damage",
							Operation:   "add_modifier",
							Modifier:    map[string]any{"type": "stat_block_section", "subtype": "modifier", "name": "+2 damage (elite)"},
						},
					},
				},
			},
		},
	}

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Check that the damage array now has the modifier appended
	actions := resp.Creature["stat_block"].(map[string]any)["offense"].(map[string]any)["offensive_actions"].([]any)
	damage := actions[0].(map[string]any)["attack"].(map[string]any)["damage"].([]any)
	if len(damage) != 2 {
		t.Fatalf("expected 2 damage entries (original + modifier), got %d", len(damage))
	}
	mod := damage[1].(map[string]any)
	if mod["name"] != "+2 damage (elite)" {
		t.Errorf("expected modifier name '+2 damage (elite)', got %v", mod["name"])
	}
}

func TestApply_AddModifier_MissingTarget(t *testing.T) {
	// When the target path doesn't exist, add_modifier is a no-op (path resolver
	// returns no results for missing fields). This is correct — the template
	// conditional guards should prevent this case in practice.
	creature := map[string]any{
		"stat_block": map[string]any{
			"offense": map[string]any{
				"offensive_actions": []any{
					map[string]any{
						"ability": map[string]any{
							"name": "Breath Weapon",
							// no "damage" field
						},
					},
				},
			},
		},
	}

	tmpl := TemplateJSON{
		MonsterTemplate: MonsterTemplate{
			Name: "Test",
			Changes: []Change{
				{
					ChangeCategory: "damage",
					Text:           "Add damage modifier to ability",
					Effects: []Effect{
						{
							Target:    "$.offense.offensive_actions[*].ability.damage",
							Operation: "add_modifier",
							Modifier:  map[string]any{"type": "stat_block_section", "subtype": "modifier", "name": "+4 damage"},
						},
					},
				},
			},
		},
	}

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// No patches should be generated (target doesn't exist)
	if len(resp.PatchDoc.AppliedPatches) != 0 {
		t.Errorf("expected no patches for missing target, got %d", len(resp.PatchDoc.AppliedPatches))
	}
}
