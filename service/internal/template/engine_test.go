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

func TestAddItem_Dedup_ObjectArray(t *testing.T) {
	// Creature already has "poison" immunity
	creature := map[string]any{
		"stat_block": map[string]any{
			"defense": map[string]any{
				"hitpoints": []any{
					map[string]any{
						"hp": float64(100),
						"immunities": []any{
							map[string]any{"name": "poison", "subtype": "immunity", "type": "stat_block_section"},
							map[string]any{"name": "disease", "subtype": "immunity", "type": "stat_block_section"},
						},
					},
				},
			},
		},
	}

	tmpl := TemplateJSON{
		Name: "Test",
		MonsterTemplate: MonsterTemplate{
			Name: "Test",
			Changes: []Change{
				{
					Text:           "Add immunities",
					ChangeCategory: "immunities",
					Effects: []Effect{
						// "poison" already exists — should be skipped
						{Target: "$.defense.hitpoints[*].immunities", Operation: "add_item",
							Item: map[string]any{"name": "poison", "subtype": "immunity", "type": "stat_block_section"}},
						// "paralyzed" is new — should be added
						{Target: "$.defense.hitpoints[*].immunities", Operation: "add_item",
							Item: map[string]any{"name": "paralyzed", "subtype": "immunity", "type": "stat_block_section"}},
					},
				},
			},
		},
	}

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	hp := resp.Creature["stat_block"].(map[string]any)["defense"].(map[string]any)["hitpoints"].([]any)[0].(map[string]any)
	immunities := hp["immunities"].([]any)

	// Should have 3 (poison, disease, paralyzed) not 4 (no duplicate poison)
	if len(immunities) != 3 {
		names := make([]string, len(immunities))
		for i, im := range immunities {
			names[i] = im.(map[string]any)["name"].(string)
		}
		t.Errorf("expected 3 immunities (dedup poison), got %d: %v", len(immunities), names)
	}
}

func TestAddItem_Dedup_StringArray(t *testing.T) {
	creature := map[string]any{
		"stat_block": map[string]any{
			"creature_type": map[string]any{
				"creature_types": []any{"Animal"},
			},
		},
	}

	tmpl := TemplateJSON{
		Name: "Test",
		MonsterTemplate: MonsterTemplate{
			Name: "Test",
			Changes: []Change{
				{
					Text:           "Add types",
					ChangeCategory: "traits",
					Effects: []Effect{
						{Target: "$.creature_type.creature_types", Operation: "add_item", Name: "Animal"}, // dup
						{Target: "$.creature_type.creature_types", Operation: "add_item", Name: "Undead"}, // new
					},
				},
			},
		},
	}

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	ct := resp.Creature["stat_block"].(map[string]any)["creature_type"].(map[string]any)
	types := ct["creature_types"].([]any)

	if len(types) != 2 {
		t.Errorf("expected 2 creature_types (dedup Animal), got %d: %v", len(types), types)
	}
}

func TestAddItem_Dedup_NameAgainstObjectArray(t *testing.T) {
	// eff.Name used to add to an array of objects — should dedup by matching name field
	creature := map[string]any{
		"stat_block": map[string]any{
			"creature_type": map[string]any{
				"creature_types": []any{
					map[string]any{"name": "Animal", "type": "trait"},
				},
			},
		},
	}

	tmpl := TemplateJSON{
		Name: "Test",
		MonsterTemplate: MonsterTemplate{
			Name: "Test",
			Changes: []Change{
				{
					Text:           "Add types",
					ChangeCategory: "traits",
					Effects: []Effect{
						{Target: "$.creature_type.creature_types", Operation: "add_item", Name: "Animal"}, // dup
						{Target: "$.creature_type.creature_types", Operation: "add_item", Name: "Ghost"},  // new
					},
				},
			},
		},
	}

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	ct := resp.Creature["stat_block"].(map[string]any)["creature_type"].(map[string]any)
	types := ct["creature_types"].([]any)

	if len(types) != 2 {
		t.Errorf("expected 2 creature_types (dedup Animal), got %d: %v", len(types), types)
	}
}

func TestWildcard_AddItem_EmptyArrayCleanup(t *testing.T) {
	// When all conditional effects fail, the eagerly created array should be removed
	creature := map[string]any{
		"stat_block": map[string]any{
			"defense": map[string]any{
				"hitpoints": []any{
					map[string]any{
						"hp": float64(100),
						// No "weaknesses" field — resolveOrCreate will create it
					},
				},
			},
			"creature_type": map[string]any{
				"level": float64(13), // Level that falls through all conditionals
			},
		},
	}

	tmpl := TemplateJSON{
		Name: "Test",
		MonsterTemplate: MonsterTemplate{
			Name: "Test",
			Changes: []Change{
				{
					Text:           "Add weaknesses",
					ChangeCategory: "weaknesses",
					Effects: []Effect{
						// Only matches level <= 3
						{
							Target: "$.defense.hitpoints[*].weaknesses", Operation: "add_item",
							Item:        map[string]any{"name": "force", "value": float64(3)},
							Conditional: "$.creature_type.level <= 3",
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

	hp := resp.Creature["stat_block"].(map[string]any)["defense"].(map[string]any)["hitpoints"].([]any)[0].(map[string]any)
	if _, exists := hp["weaknesses"]; exists {
		t.Errorf("expected weaknesses to be absent (all conditionals failed), got %v", hp["weaknesses"])
	}
}

func TestApplyMonsterFamilyRules(t *testing.T) {
	// Families carry changes under "monster_family"; the engine must apply
	// them identically to template changes (legacy Lich: level +1).
	tmplJSON := []byte(`{
		"name": "Lich",
		"monster_family": {
			"name": "Lich",
			"changes": [{
				"change_category": "level",
				"text": "Increase the spellcaster's level by 1.",
				"effects": [{
					"operation": "adjustment",
					"target": "$.creature_type.level",
					"value": 1
				}]
			}]
		}
	}`)
	var tmpl TemplateJSON
	if err := json.Unmarshal(tmplJSON, &tmpl); err != nil {
		t.Fatal(err)
	}
	creature := map[string]any{
		"name": "Test Caster",
		"stat_block": map[string]any{
			"creature_type": map[string]any{"level": float64(11)},
		},
	}
	result, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	sb := result.Creature["stat_block"].(map[string]any)
	ct := sb["creature_type"].(map[string]any)
	if lvl := ct["level"]; lvl != float64(12) {
		t.Fatalf("family level change not applied: got %v want 12", lvl)
	}
}

func TestRulesPrefersFamilyWhenPresent(t *testing.T) {
	fam := MonsterTemplate{Name: "Fam"}
	tj := TemplateJSON{MonsterTemplate: MonsterTemplate{Name: "Tmpl"}, MonsterFamily: &fam}
	if tj.Rules().Name != "Fam" {
		t.Fatal("Rules() must prefer monster_family")
	}
	tj2 := TemplateJSON{MonsterTemplate: MonsterTemplate{Name: "Tmpl"}}
	if tj2.Rules().Name != "Tmpl" {
		t.Fatal("Rules() must fall back to monster_template")
	}
}

func TestApplyAllRealFamilies(t *testing.T) {
	// Corpus smoke: every real family file must Apply without error
	// against a minimal creature. Skips when the data checkout is absent
	// (CI has no pfsrd2-data).
	root := "/home/devon/MasterworkTools/pfsrd2/pfsrd2-data/monster_families"
	if _, err := os.Stat(root); err != nil {
		t.Skip("pfsrd2-data checkout not present")
	}
	creature := map[string]any{
		"name": "Smoke",
		"stat_block": map[string]any{
			"creature_type": map[string]any{"level": float64(5)},
			"defense":       map[string]any{},
		},
	}
	n := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var tmpl TemplateJSON
		if err := json.Unmarshal(raw, &tmpl); err != nil {
			t.Errorf("%s: unmarshal: %v", path, err)
			return nil
		}
		if _, err := Apply(deepCopyMap(creature), tmpl); err != nil {
			t.Errorf("%s: apply: %v", path, err)
		}
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 500 {
		t.Fatalf("expected 600+ family files, walked %d", n)
	}
}

func deepCopyMap(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func TestSectionSourceAddItems(t *testing.T) {
	// Nymph queen grant: the change's source points at a section ability
	// pool rather than change/template abilities.
	tmplJSON := []byte(`{
		"name": "Nymph",
		"monster_family": {
			"name": "Nymph",
			"changes": [{
				"change_category": "abilities",
				"text": "She gains the Inspiration ability.",
				"effects": [{
					"operation": "add_items",
					"target": "$.defense.automatic_abilities",
					"source": "$.sections[?(@.name=='Nymph Queen Abilities')].abilities"
				}]
			}]
		},
		"sections": [{
			"name": "Creating Nymph Queens",
			"sections": [{
				"name": "Nymph Queen Abilities",
				"abilities": [
					{"name": "Inspiration", "subtype": "ability", "type": "stat_block_section"},
					{"name": "Change Shape", "subtype": "ability", "type": "stat_block_section"}
				]
			}]
		}]
	}`)
	var tmpl TemplateJSON
	if err := json.Unmarshal(tmplJSON, &tmpl); err != nil {
		t.Fatal(err)
	}
	creature := map[string]any{
		"name":       "Dryad",
		"stat_block": map[string]any{"defense": map[string]any{}},
	}
	result, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	sb := result.Creature["stat_block"].(map[string]any)
	auto := sb["defense"].(map[string]any)["automatic_abilities"].([]any)
	if len(auto) != 2 {
		t.Fatalf("expected 2 granted abilities, got %d", len(auto))
	}
}

func TestSectionSourceNoMatchErrors(t *testing.T) {
	tmpl := TemplateJSON{MonsterFamily: &MonsterTemplate{Changes: []Change{{
		ChangeCategory: "abilities",
		Effects: []Effect{{Operation: "add_items",
			Target: "$.defense.automatic_abilities",
			Source: "$.sections[?(@.name=='Missing')].abilities"}},
	}}}}
	creature := map[string]any{"stat_block": map[string]any{}}
	if _, err := Apply(creature, tmpl); err == nil {
		t.Fatal("expected error for unmatched sections source")
	}
}

func TestAddItemRaisesExistingLowerSkill(t *testing.T) {
	// Amphibious: "Add Athletics with a modifier equal to the creature's
	// highest skill modifier" — Stingray has Athletics +5, Stealth +7:
	// Athletics must RAISE to 7. An already-higher value stays. Non-value
	// items keep dedup-skip.
	tmplJSON := []byte(`{
		"name": "Amphibious",
		"monster_template": {
			"name": "Amphibious",
			"changes": [{
				"change_category": "skills",
				"text": "- Add Athletics with a modifier equal to the creature's highest skill modifier.",
				"effects": [{
					"operation": "add_item",
					"target": "$.statistics.skills",
					"item": {"name": "Athletics", "subtype": "skill", "type": "stat_block_section"},
					"value_from": "$.statistics.skills[*].value | max"
				}]
			}]
		}
	}`)
	var tmpl TemplateJSON
	if err := json.Unmarshal(tmplJSON, &tmpl); err != nil {
		t.Fatal(err)
	}
	mk := func(ath float64) map[string]any {
		return map[string]any{
			"name": "Stingray",
			"stat_block": map[string]any{
				"statistics": map[string]any{
					"skills": []any{
						map[string]any{"name": "Athletics", "value": ath},
						map[string]any{"name": "Stealth", "value": float64(7)},
					},
				},
			},
		}
	}
	result, err := Apply(mk(5), tmpl)
	if err != nil {
		t.Fatal(err)
	}
	skills := result.Creature["stat_block"].(map[string]any)["statistics"].(map[string]any)["skills"].([]any)
	for _, sk := range skills {
		m := sk.(map[string]any)
		if m["name"] == "Athletics" {
			if v, _ := toFloat64(m["value"]); v != 7 {
				t.Fatalf("Athletics not raised: got %v want 7", m["value"])
			}
		}
	}
	// already-higher stays
	result, err = Apply(mk(9), tmpl)
	if err != nil {
		t.Fatal(err)
	}
	skills = result.Creature["stat_block"].(map[string]any)["statistics"].(map[string]any)["skills"].([]any)
	for _, sk := range skills {
		m := sk.(map[string]any)
		if m["name"] == "Athletics" {
			if v, _ := toFloat64(m["value"]); v != 9 {
				t.Fatalf("higher Athletics clobbered: got %v want 9", m["value"])
			}
		}
	}
}

func TestApplyWithSelectionsOptionIndices(t *testing.T) {
	// Structured select (mutant-cryptid style): answering with an option
	// index applies that option's effect and attributes the patch group.
	tmplJSON := []byte(`{
		"name": "Mutant",
		"monster_template": {
			"name": "Mutant",
			"changes": [{
				"change_category": "traits",
				"text": "Add one of the following traits.",
				"effects": [{
					"operation": "select",
					"target": "$.creature_type.creature_types",
					"selection": {
						"min": 1, "max": 1,
						"options": [
							{"operation": "add_item", "target": "$.creature_type.creature_types", "name": "Ooze"},
							{"operation": "add_item", "target": "$.creature_type.creature_types", "name": "Plant"}
						]
					}
				}]
			}]
		}
	}`)
	var tmpl TemplateJSON
	if err := json.Unmarshal(tmplJSON, &tmpl); err != nil {
		t.Fatal(err)
	}
	creature := map[string]any{
		"stat_block": map[string]any{
			"creature_type": map[string]any{"creature_types": []any{"Animal"}},
		},
	}
	res, err := ApplyWithSelections(deepCopyMap(creature), tmpl, []SelectionChoice{
		{ID: "c0/e0", OptionIndices: []int{1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ct := res.Creature["stat_block"].(map[string]any)["creature_type"].(map[string]any)
	types := ct["creature_types"].([]any)
	found := false
	for _, v := range types {
		if v == "Plant" {
			found = true
		}
		if v == "Ooze" {
			t.Fatal("unchosen option applied")
		}
	}
	if !found {
		t.Fatalf("chosen option not applied: %v", types)
	}
	var selGroup *PatchGroup
	for i := range res.PatchDoc.AppliedPatches {
		if res.PatchDoc.AppliedPatches[i].SelectionID == "c0/e0" {
			selGroup = &res.PatchDoc.AppliedPatches[i]
		}
	}
	if selGroup == nil {
		t.Fatal("selection patch group missing")
	}
	if len(res.PatchDoc.AppliedSelections) != 1 || res.PatchDoc.AppliedSelections[0] != "c0/e0" {
		t.Fatalf("applied_selections wrong: %v", res.PatchDoc.AppliedSelections)
	}
}

func TestApplyWithSelectionsClientEffects(t *testing.T) {
	// Descriptor select (Air spell swap): the client computes the effects.
	tmplJSON := []byte(`{
		"name": "Air",
		"monster_template": {
			"name": "Air",
			"changes": [{
				"change_category": "spells",
				"text": "Replace spells with air spells of the same rank.",
				"effects": [{
					"operation": "select",
					"target": "$.offense.offensive_actions",
					"selection": {"description": "swap spells", "constraint": "same rank"}
				}]
			}]
		}
	}`)
	var tmpl TemplateJSON
	if err := json.Unmarshal(tmplJSON, &tmpl); err != nil {
		t.Fatal(err)
	}
	creature := map[string]any{
		"stat_block": map[string]any{
			"statistics": map[string]any{
				"languages": map[string]any{"languages": []any{}},
			},
		},
	}
	res, err := ApplyWithSelections(deepCopyMap(creature), tmpl, []SelectionChoice{
		{ID: "c0/e0", Effects: []Effect{{
			Operation: "add_item",
			Target:    "$.statistics.languages.languages",
			Name:      "Sussuran",
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	langs := res.Creature["stat_block"].(map[string]any)["statistics"].(map[string]any)["languages"].(map[string]any)["languages"].([]any)
	if len(langs) != 1 {
		t.Fatalf("client effect not applied: %v", langs)
	}
	// unknown id errors
	if _, err := ApplyWithSelections(deepCopyMap(creature), tmpl, []SelectionChoice{{ID: "c9/e9"}}); err == nil {
		t.Fatal("unknown selection id must error")
	}
	// out-of-range option errors
	if _, err := ApplyWithSelections(deepCopyMap(creature), tmpl, []SelectionChoice{{ID: "c0/e0", OptionIndices: []int{3}}}); err == nil {
		t.Fatal("out-of-range option must error")
	}
}
