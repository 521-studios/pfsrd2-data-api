package template

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadTemplateFile loads a template from the pfsrd2-data directory.
func loadTemplateFile(t *testing.T, book, name string) TemplateJSON {
	t.Helper()
	path := filepath.Join(fixtureDir(), "monster_templates", book, name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixture not available: %v", err)
	}
	var tmpl TemplateJSON
	if err := json.Unmarshal(data, &tmpl); err != nil {
		t.Fatalf("parse template %s/%s: %v", book, name, err)
	}
	return tmpl
}

// loadCreatureFile loads a creature from the pfsrd2-data directory.
// typDir is the content type directory (e.g. "monsters", "npcs").
func loadCreatureFile(t *testing.T, typDir, book, name string) map[string]any {
	t.Helper()
	path := filepath.Join(fixtureDir(), typDir, book, name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("fixture not available: %v", err)
	}
	var creature map[string]any
	if err := json.Unmarshal(data, &creature); err != nil {
		t.Fatalf("parse creature %s/%s: %v", book, name, err)
	}
	return creature
}

// --- Experimental Cryptids (adjustment + add_modifier only) ---

func TestApply_ExperimentalCryptids_BoggardScout(t *testing.T) {
	creature := loadCreatureFile(t, "monsters", "monster_core", "boggard_scout")
	tmpl := loadTemplateFile(t, "dark_archive", "experimental_cryptids")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Boggard Scout L1: AC 16, Fort +9, Ref +5, Will +7, HP 24
	// Experimental cryptids: +1 to AC/attacks/DCs/saves/perception/skills
	ac := getStatBlockValue(t, resp.Creature, "defense", "ac", "value")
	if ac != float64(17) {
		t.Errorf("expected AC 17 (16+1), got %v", ac)
	}

	fort := getStatBlockValue(t, resp.Creature, "defense", "saves", "fort", "value")
	if fort != float64(10) {
		t.Errorf("expected Fort 10 (9+1), got %v", fort)
	}

	// HP: level 1, so +10 HP → 24+10=34
	hpArr := getStatBlockValue(t, resp.Creature, "defense", "hitpoints")
	hp := hpArr.([]any)[0].(map[string]any)["hp"]
	if hp != float64(34) {
		t.Errorf("expected HP 34 (24+10), got %v", hp)
	}

	if len(resp.PatchDoc.AppliedPatches) == 0 {
		t.Error("expected patches")
	}
}

// --- Skeleton template (add_item, add_items, adjustment) ---

func TestApply_Skeleton_BoggardScout(t *testing.T) {
	creature := loadCreatureFile(t, "monsters", "monster_core", "boggard_scout")
	tmpl := loadTemplateFile(t, "book_of_the_dead", "skeleton")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Check immunities were added
	hpArr := getStatBlockValue(t, resp.Creature, "defense", "hitpoints")
	hpSlice := hpArr.([]any)
	immunities := hpSlice[0].(map[string]any)["immunities"]
	if immunities == nil {
		t.Fatal("expected immunities to be added")
	}
	immArr := immunities.([]any)
	// Skeleton adds: death effects, disease, paralyzed, poison, unconscious
	foundImmunities := map[string]bool{}
	for _, imm := range immArr {
		if m, ok := imm.(map[string]any); ok {
			if name, ok := m["name"].(string); ok {
				foundImmunities[name] = true
			}
		}
	}
	for _, want := range []string{"death effects", "disease", "paralyzed", "poison", "unconscious"} {
		if !foundImmunities[want] {
			t.Errorf("missing immunity: %s", want)
		}
	}

	// Check resistances were added
	resistances := hpSlice[0].(map[string]any)["resistances"]
	if resistances == nil {
		t.Fatal("expected resistances to be added")
	}

	// HP should be reduced (L1: -2)
	hp := hpSlice[0].(map[string]any)["hp"].(float64)
	if hp != 22 { // 24 - 2
		t.Errorf("expected HP 22 (24-2 for L1 skeleton), got %v", hp)
	}

	// Check abilities were added (via add_items)
	// Abilities may be at defense.automatic_abilities or defense.hitpoints[0].automatic_abilities
	autoAbilities := getStatBlockValue(t, resp.Creature, "defense", "automatic_abilities")
	if autoAbilities == nil {
		// Check inside hitpoints (newer template data puts abilities here)
		autoAbilities = hpSlice[0].(map[string]any)["automatic_abilities"]
	}
	if autoAbilities == nil {
		t.Fatal("expected automatic_abilities")
	}
	aaArr := autoAbilities.([]any)
	foundAbilities := map[string]bool{}
	for _, a := range aaArr {
		if m, ok := a.(map[string]any); ok {
			if name, ok := m["name"].(string); ok {
				foundAbilities[name] = true
			}
		}
	}
	// Also check special_senses (Darkvision is routed there)
	senses := getStatBlockValue(t, resp.Creature, "senses")
	if sensesMap, ok := senses.(map[string]any); ok {
		if ss, ok := sensesMap["special_senses"].([]any); ok {
			for _, s := range ss {
				if m, ok := s.(map[string]any); ok {
					if name, ok := m["name"].(string); ok {
						foundAbilities[name] = true
					}
				}
			}
		}
	}

	// Skeleton adds Darkvision (→ special_senses) and Negative Healing (→ automatic_abilities)
	// Check case-insensitively since creature data may use different casing
	for _, want := range []string{"Darkvision", "Negative Healing"} {
		found := false
		for name := range foundAbilities {
			if strings.EqualFold(name, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing ability: %s (found: %v)", want, foundAbilities)
		}
	}

	if len(resp.PatchDoc.AppliedPatches) == 0 {
		t.Error("expected patches")
	}
}

// --- Ghost template (add_item, replace, add_items, replace_highest_with) ---
// Note: replace_highest_with not yet implemented, but add_item/replace should work

func TestApply_Ghost_BoggardScout(t *testing.T) {
	creature := loadCreatureFile(t, "monsters", "monster_core", "boggard_scout")
	tmpl := loadTemplateFile(t, "book_of_the_dead", "ghost")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Ghost adds immunities
	hpArr := getStatBlockValue(t, resp.Creature, "defense", "hitpoints")
	immunities := hpArr.([]any)[0].(map[string]any)["immunities"]
	if immunities == nil {
		t.Fatal("expected immunities")
	}
	immArr := immunities.([]any)
	foundDeathEffects := false
	for _, imm := range immArr {
		if m, ok := imm.(map[string]any); ok {
			if m["name"] == "death effects" {
				foundDeathEffects = true
			}
		}
	}
	if !foundDeathEffects {
		t.Error("expected 'death effects' immunity")
	}

	// Ghost adds weaknesses (force, ghost touch, positive — conditional on level)
	weaknesses := hpArr.([]any)[0].(map[string]any)["weaknesses"]
	if weaknesses == nil {
		t.Fatal("expected weaknesses")
	}

	// Ghost replaces all melee damage types to "negative"
	// Check that attack damage types were changed
	actions := getStatBlockValue(t, resp.Creature, "offense", "offensive_actions")
	if actions == nil {
		t.Fatal("expected offensive_actions")
	}
	for _, a := range actions.([]any) {
		action := a.(map[string]any)
		if atk, ok := action["attack"].(map[string]any); ok {
			for _, d := range atk["damage"].([]any) {
				dmg := d.(map[string]any)
				if dt, ok := dmg["damage_type"].(string); ok {
					if dt != "negative" {
						t.Errorf("expected damage_type 'negative', got %q", dt)
					}
				}
			}
		}
	}

	if len(resp.PatchDoc.AppliedPatches) == 0 {
		t.Error("expected patches")
	}
}

// --- Replace operation unit test ---

func TestApply_Replace(t *testing.T) {
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
			Changes: []Change{{
				ChangeCategory: "damage",
				Text:           "Replace damage type",
				Effects: []Effect{{
					Target:    "$.offense.offensive_actions[*].attack.damage[*].damage_type",
					Operation: "replace",
					Value:     "negative",
				}},
			}},
		},
	}

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	actions := resp.Creature["stat_block"].(map[string]any)["offense"].(map[string]any)["offensive_actions"].([]any)
	dmg := actions[0].(map[string]any)["attack"].(map[string]any)["damage"].([]any)
	dt := dmg[0].(map[string]any)["damage_type"]
	if dt != "negative" {
		t.Errorf("expected damage_type 'negative', got %v", dt)
	}
}

// --- Zombie template (add_item, add_items, adjustment) ---

func TestApply_Zombie_GiantGecko(t *testing.T) {
	creature := loadCreatureFile(t, "monsters", "monster_core", "giant_gecko")
	tmpl := loadTemplateFile(t, "book_of_the_dead", "zombie")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Zombie adds immunities
	hpArr := getStatBlockValue(t, resp.Creature, "defense", "hitpoints")
	immunities := hpArr.([]any)[0].(map[string]any)["immunities"]
	if immunities == nil {
		t.Fatal("expected immunities")
	}

	if len(resp.PatchDoc.AppliedPatches) == 0 {
		t.Error("expected patches")
	}
}

// --- Orc NPC template (remove_item, add_item, add_items) ---

func TestApply_OrcNPC_Adept(t *testing.T) {
	creature := loadCreatureFile(t, "npcs", "npc_core", "adept")
	tmpl := loadTemplateFile(t, "npc_core", "orc")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Check "Human" removed and "Orc" added to creature_types
	ctypes := getStatBlockValue(t, resp.Creature, "creature_type", "creature_types")
	typeArr := ctypes.([]any)
	hasHuman := false
	hasOrc := false
	for _, ct := range typeArr {
		if ct == "Human" {
			hasHuman = true
		}
		if ct == "Orc" {
			hasOrc = true
		}
	}
	if hasHuman {
		t.Error("Human should have been removed from creature_types")
	}
	if !hasOrc {
		t.Error("Orc should have been added to creature_types")
	}

	// Check Orcish language added
	langs := getStatBlockValue(t, resp.Creature, "statistics", "languages", "languages")
	langArr := langs.([]any)
	hasOrcish := false
	for _, l := range langArr {
		if m, ok := l.(map[string]any); ok {
			if m["name"] == "Orcish" {
				hasOrcish = true
			}
		}
	}
	if !hasOrcish {
		t.Error("Orcish language should have been added")
	}

	if len(resp.PatchDoc.AppliedPatches) == 0 {
		t.Error("expected patches")
	}
}

// --- Corrupt NPC template (adjustment, add_item) ---

func TestApply_Corrupt_Adept(t *testing.T) {
	creature := loadCreatureFile(t, "npcs", "npc_core", "adept")
	tmpl := loadTemplateFile(t, "npc_core", "corrupt")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(resp.PatchDoc.AppliedPatches) == 0 {
		t.Error("expected patches")
	}
}

// --- Dwarf NPC template (replace with filter, remove_item, add_item, add_items) ---

func TestApply_DwarfNPC_Adept(t *testing.T) {
	creature := loadCreatureFile(t, "npcs", "npc_core", "adept")
	tmpl := loadTemplateFile(t, "npc_core", "dwarf")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Dwarf replaces walk speed with 20 (if > 20)
	speeds := getStatBlockValue(t, resp.Creature, "offense", "speed", "movement")
	for _, s := range speeds.([]any) {
		sm := s.(map[string]any)
		if sm["movement_type"] == "walk" {
			if sm["value"] != float64(20) {
				t.Errorf("expected walk speed 20 (dwarf), got %v", sm["value"])
			}
		}
	}

	// Human removed, Dwarf added
	ctypes := getStatBlockValue(t, resp.Creature, "creature_type", "creature_types")
	hasHuman := false
	hasDwarf := false
	for _, ct := range ctypes.([]any) {
		if ct == "Human" {
			hasHuman = true
		}
		if ct == "Dwarf" {
			hasDwarf = true
		}
	}
	if hasHuman {
		t.Error("Human should be removed")
	}
	if !hasDwarf {
		t.Error("Dwarf should be added")
	}

	if len(resp.PatchDoc.AppliedPatches) == 0 {
		t.Error("expected patches")
	}
}

// --- Path filter tests ---

func TestResolvePath_Filter(t *testing.T) {
	sb := map[string]any{
		"offense": map[string]any{
			"speed": map[string]any{
				"movement": []any{
					map[string]any{"movement_type": "walk", "value": float64(25)},
					map[string]any{"movement_type": "swim", "value": float64(30)},
				},
			},
		},
	}

	results, err := resolvePath(sb, "$.offense.speed.movement[?(@.movement_type=='walk')].value")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Get() != float64(25) {
		t.Errorf("expected 25, got %v", results[0].Get())
	}
}

func TestResolvePath_FilterStringArray(t *testing.T) {
	sb := map[string]any{
		"creature_type": map[string]any{
			"creature_types": []any{"Human", "Humanoid"},
		},
	}

	results, err := resolvePath(sb, "$.creature_type.creature_types[?(@ == 'Human')]")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Get() != "Human" {
		t.Errorf("expected 'Human', got %v", results[0].Get())
	}
}

// --- NPC Guerrilla template (adjustment, add_modifier, add_item) ---

func TestApply_Guerrilla_BoggardScout(t *testing.T) {
	creature := loadCreatureFile(t, "monsters", "monster_core", "boggard_scout")
	tmpl := loadTemplateFile(t, "npc_core", "guerrilla")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(resp.PatchDoc.AppliedPatches) == 0 {
		t.Error("expected patches")
	}
}

// --- Vampire template (add_item, adjustment, select) ---

func TestApply_Vampire_BoggardScout(t *testing.T) {
	creature := loadCreatureFile(t, "monsters", "monster_core", "boggard_scout")
	tmpl := loadTemplateFile(t, "book_of_the_dead", "vampire")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Vampire has select operations — should appear in selections
	if len(resp.PatchDoc.Selections) == 0 {
		t.Error("expected selections for vampire template")
	}

	if len(resp.PatchDoc.AppliedPatches) == 0 {
		t.Error("expected patches")
	}
}

// --- Amphibious template (add_item with value_from, add_items) ---

func TestApply_Amphibious_BoggardScout(t *testing.T) {
	creature := loadCreatureFile(t, "monsters", "monster_core", "boggard_scout")
	tmpl := loadTemplateFile(t, "howl_of_the_wild", "amphibious")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(resp.PatchDoc.AppliedPatches) == 0 {
		t.Error("expected patches")
	}
}

// --- Fire elemental template (add_item, replace_one_die, select) ---

func TestApply_FireElemental_BoggardScout(t *testing.T) {
	creature := loadCreatureFile(t, "monsters", "monster_core", "boggard_scout")
	tmpl := loadTemplateFile(t, "rage_of_elements", "fire")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(resp.PatchDoc.Selections) == 0 {
		t.Error("expected selections for fire template")
	}
}

// --- Broodpiercer template (adjustment, add_modifier, replace) ---

func TestApply_Broodpiercer_GiantGecko(t *testing.T) {
	creature := loadCreatureFile(t, "monsters", "monster_core", "giant_gecko")
	tmpl := loadTemplateFile(t, "howl_of_the_wild", "broodpiercer")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(resp.PatchDoc.AppliedPatches) == 0 {
		t.Error("expected patches")
	}
}

// --- Frostbound template (add_item, add_items) ---

func TestApply_Frostbound_BoggardScout(t *testing.T) {
	creature := loadCreatureFile(t, "monsters", "monster_core", "boggard_scout")
	tmpl := loadTemplateFile(t, "howl_of_the_wild", "frostbound")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(resp.PatchDoc.AppliedPatches) == 0 {
		t.Error("expected patches")
	}
}

// --- Miniature template (set_reach, replace, add_item) ---

func TestApply_Miniature_BoggardScout(t *testing.T) {
	creature := loadCreatureFile(t, "monsters", "monster_core", "boggard_scout")
	tmpl := loadTemplateFile(t, "howl_of_the_wild", "miniature")

	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Miniature sets size to Tiny
	size := getStatBlockValue(t, resp.Creature, "creature_type", "size")
	if size != "Tiny" {
		t.Errorf("expected size Tiny, got %v", size)
	}

	if len(resp.PatchDoc.AppliedPatches) == 0 {
		t.Error("expected patches")
	}
}

// --- value_from unit tests ---

func TestEvaluateValueFrom_Max(t *testing.T) {
	sb := map[string]any{
		"statistics": map[string]any{
			"skills": []any{
				map[string]any{"name": "Athletics", "value": float64(10)},
				map[string]any{"name": "Stealth", "value": float64(15)},
				map[string]any{"name": "Acrobatics", "value": float64(8)},
			},
		},
	}
	val, err := evaluateValueFrom(sb, "$.statistics.skills[*].value | max", nil)
	if err != nil {
		t.Fatal(err)
	}
	if val != float64(15) {
		t.Errorf("expected 15, got %v", val)
	}
}

func TestEvaluateValueFrom_Division(t *testing.T) {
	sb := map[string]any{
		"offense": map[string]any{
			"speed": map[string]any{
				"movement": []any{
					map[string]any{"movement_type": "walk", "value": float64(30)},
				},
			},
		},
	}
	min15 := float64(15)
	val, err := evaluateValueFrom(sb, "$.offense.speed.movement[?(@.movement_type=='walk')].value / 2", &min15)
	if err != nil {
		t.Fatal(err)
	}
	if val != float64(15) {
		t.Errorf("expected 15 (30/2=15, min 15), got %v", val)
	}

	// Test with value above minimum
	sb["offense"].(map[string]any)["speed"].(map[string]any)["movement"].([]any)[0].(map[string]any)["value"] = float64(50)
	val, err = evaluateValueFrom(sb, "$.offense.speed.movement[?(@.movement_type=='walk')].value / 2", &min15)
	if err != nil {
		t.Fatal(err)
	}
	if val != float64(25) {
		t.Errorf("expected 25 (50/2=25), got %v", val)
	}
}

func TestEvaluateValueFrom_HighForLevel(t *testing.T) {
	sb := map[string]any{
		"creature_type": map[string]any{"level": float64(5)},
		"statistics":    map[string]any{"skills": []any{}},
	}
	val, err := evaluateValueFrom(sb, "$.statistics.skills | high_for_level", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Level 5 HIGH skill = 13 (GM Core p.116 skills table; 16 is extreme)
	if val != float64(13) {
		t.Errorf("expected 13 for level 5 high_for_level, got %v", val)
	}
}
