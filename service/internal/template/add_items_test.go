package template

import (
	"encoding/json"
	"reflect"
	"testing"
)

func ability(name, category string) map[string]any {
	return map[string]any{
		"ability_category": category,
		"ability_type":     "ability",
		"name":             name,
		"type":             "stat_block_section",
	}
}

func baseStatBlock() map[string]any {
	return map[string]any{
		"creature_type": map[string]any{"level": float64(3)},
		"senses":        map[string]any{},
		"defense": map[string]any{
			"hitpoints": []any{map[string]any{"hp": float64(30)}},
		},
		"offense": map[string]any{"offensive_actions": []any{}},
	}
}

func namesOf(v any) []string {
	arr, _ := v.([]any)
	var out []string
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m["name"].(string))
		}
	}
	return out
}

func TestAddItems_TopLevelAbilitiesPool(t *testing.T) {
	// NPC Core shape: abilities at monster_template.abilities, source still
	// says $.changes[*].abilities — the pool must cover both.
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Abilities: []any{ability("Low-Light Vision", "special_sense")},
		Changes: []Change{{
			ChangeCategory: "abilities",
			Effects: []Effect{{
				Operation: "add_items",
				Source:    "$.changes[*].abilities",
				Target:    "$.senses.special_senses",
			}},
		}},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sb := resp.Creature["stat_block"].(map[string]any)
	got := namesOf(sb["senses"].(map[string]any)["special_senses"])
	if !reflect.DeepEqual(got, []string{"Low-Light Vision"}) {
		t.Errorf("special_senses = %v", got)
	}
}

func TestAddItems_MultiEffectGroupSameTarget(t *testing.T) {
	// Frostbound shape: several add_items effects sharing one target used to
	// fall through to "unsupported operation: add_items".
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Changes: []Change{{
			ChangeCategory: "abilities",
			Abilities: []any{
				ability("Freezing Adaptation", "automatic"),
				ability("Winter Senses", "automatic"),
				ability("Snow Spray", "offensive"),
			},
			Effects: []Effect{
				{Operation: "add_items", Source: "$.changes[*].abilities[?(@.name=='Freezing Adaptation')]", Target: "$.defense.automatic_abilities"},
				{Operation: "add_items", Source: "$.changes[*].abilities[?(@.name=='Winter Senses')]", Target: "$.defense.automatic_abilities"},
				{Operation: "add_items", Source: "$.changes[*].abilities[?(@.name=='Snow Spray')]", Target: "$.offense.offensive_actions"},
			},
		}},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sb := resp.Creature["stat_block"].(map[string]any)
	auto := namesOf(sb["defense"].(map[string]any)["automatic_abilities"])
	if !reflect.DeepEqual(auto, []string{"Freezing Adaptation", "Winter Senses"}) {
		t.Errorf("automatic_abilities = %v", auto)
	}
	off := namesOf(sb["offense"].(map[string]any)["offensive_actions"])
	if !reflect.DeepEqual(off, []string{"Snow Spray"}) {
		t.Errorf("offensive_actions = %v", off)
	}
}

func TestAddItems_UnfilteredSourceDivertsSenseToSpecialSenses(t *testing.T) {
	// The real NPC Core shape: one unfiltered add_items targeting
	// automatic_abilities, with a known sense in the pool. The sense must
	// land in special_senses (where creatures keep it), not be dropped, and
	// non-sense abilities go to the effect's target.
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Abilities: []any{
			ability("Darkvision", "special_sense"),
			ability("Boulder Roll", "offensive"),
		},
		Changes: []Change{{
			ChangeCategory: "abilities",
			Effects: []Effect{{
				Operation: "add_items",
				Source:    "$.changes[*].abilities",
				Target:    "$.defense.automatic_abilities",
			}},
		}},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sb := resp.Creature["stat_block"].(map[string]any)
	senses := namesOf(sb["senses"].(map[string]any)["special_senses"])
	if !reflect.DeepEqual(senses, []string{"Darkvision"}) {
		t.Errorf("special_senses = %v", senses)
	}
	auto := namesOf(sb["defense"].(map[string]any)["automatic_abilities"])
	if !reflect.DeepEqual(auto, []string{"Boulder Roll"}) {
		t.Errorf("automatic_abilities = %v", auto)
	}
}

func TestAddItems_FilterOverridesSenseRouting(t *testing.T) {
	// An explicit name filter wins over the sense heuristic: a known sense
	// filtered into a non-sense container stays where the filter put it.
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Changes: []Change{{
			ChangeCategory: "abilities",
			Abilities:      []any{ability("Darkvision", "special_sense")},
			Effects: []Effect{{
				Operation: "add_items",
				Source:    "$.changes[*].abilities[?(@.name=='Darkvision')]",
				Target:    "$.defense.automatic_abilities",
			}},
		}},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sb := resp.Creature["stat_block"].(map[string]any)
	auto := namesOf(sb["defense"].(map[string]any)["automatic_abilities"])
	if !reflect.DeepEqual(auto, []string{"Darkvision"}) {
		t.Errorf("automatic_abilities = %v", auto)
	}
	if senses := sb["senses"].(map[string]any)["special_senses"]; senses != nil {
		t.Errorf("special_senses should be untouched, got %v", senses)
	}
}

func TestAddItems_FilterMatchingNothingErrors(t *testing.T) {
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Changes: []Change{{
			ChangeCategory: "abilities",
			Effects: []Effect{{
				Operation: "add_items",
				Source:    "$.changes[*].abilities[?(@.name=='No Such Ability')]",
				Target:    "$.defense.automatic_abilities",
			}},
		}},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	if _, err := Apply(creature, tmpl); err == nil {
		t.Error("expected error for filter matching no pool ability")
	}
}

func TestAddItems_ApostropheNameSurvivesSynthesis(t *testing.T) {
	// Ability-only template with a quoted name: the synthesized effect
	// carries the ability directly, so no string filter can garble it.
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Abilities: []any{ability("Hunter's Instinct", "reactive")},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sb := resp.Creature["stat_block"].(map[string]any)
	got := namesOf(sb["defense"].(map[string]any)["reactive_abilities"])
	if !reflect.DeepEqual(got, []string{"Hunter's Instinct"}) {
		t.Errorf("reactive_abilities = %v", got)
	}
}

func TestAddItems_UnknownCategoryFallback(t *testing.T) {
	// Missing/unknown ability_category: known sense names route to
	// special_senses, everything else defaults to automatic_abilities.
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Abilities: []any{
			ability("Tremorsense", ""),
			ability("Mystery Power", "brand_new_category"),
		},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sb := resp.Creature["stat_block"].(map[string]any)
	senses := namesOf(sb["senses"].(map[string]any)["special_senses"])
	if !reflect.DeepEqual(senses, []string{"Tremorsense"}) {
		t.Errorf("special_senses = %v", senses)
	}
	auto := namesOf(sb["defense"].(map[string]any)["automatic_abilities"])
	if !reflect.DeepEqual(auto, []string{"Mystery Power"}) {
		t.Errorf("automatic_abilities = %v", auto)
	}
}

func TestAddItems_PoolUnionBothLevels(t *testing.T) {
	// Change-level and top-level abilities coexist in the pool, and having
	// changes means no synthesis: only referenced abilities are applied.
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Abilities: []any{ability("Top Ability", "automatic")},
		Changes: []Change{{
			ChangeCategory: "abilities",
			Abilities:      []any{ability("Change Ability", "automatic")},
			Effects: []Effect{
				{Operation: "add_items", Source: "$.changes[*].abilities[?(@.name=='Top Ability')]", Target: "$.defense.automatic_abilities"},
				{Operation: "add_items", Source: "$.changes[*].abilities[?(@.name=='Change Ability')]", Target: "$.defense.automatic_abilities"},
			},
		}},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sb := resp.Creature["stat_block"].(map[string]any)
	auto := namesOf(sb["defense"].(map[string]any)["automatic_abilities"])
	if !reflect.DeepEqual(auto, []string{"Top Ability", "Change Ability"}) {
		t.Errorf("automatic_abilities = %v", auto)
	}
	if ia := sb["interaction_abilities"]; ia != nil {
		t.Errorf("no synthesis should run when changes exist, got %v", ia)
	}
}

func TestAddItems_SourceFilterKeepsSenseOutOfWrongTarget(t *testing.T) {
	// Two filtered effects routing to the containers the heuristic would
	// also pick: verifies no cross-leak between targets.
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Changes: []Change{{
			ChangeCategory: "abilities",
			Abilities: []any{
				ability("Darkvision", "special_sense"),
				ability("Ghostly Passage", "offensive"),
			},
			Effects: []Effect{
				{Operation: "add_items", Source: "$.changes[*].abilities[?(@.name=='Darkvision')]", Target: "$.senses.special_senses"},
				{Operation: "add_items", Source: "$.changes[*].abilities[?(@.name=='Ghostly Passage')]", Target: "$.offense.offensive_actions"},
			},
		}},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sb := resp.Creature["stat_block"].(map[string]any)
	senses := namesOf(sb["senses"].(map[string]any)["special_senses"])
	if !reflect.DeepEqual(senses, []string{"Darkvision"}) {
		t.Errorf("special_senses = %v", senses)
	}
	off := namesOf(sb["offense"].(map[string]any)["offensive_actions"])
	if !reflect.DeepEqual(off, []string{"Ghostly Passage"}) {
		t.Errorf("offensive_actions = %v", off)
	}
}

func TestAddItems_ImplicitChangeForAbilityOnlyTemplate(t *testing.T) {
	// Twinned/Tengu/Mythic shape: no changes at all, only top-level abilities.
	// The engine synthesizes an abilities change routed by ability_category.
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Abilities: []any{
			ability("Independent Brains", "interaction"),
			ability("Head Bite", "reactive"),
			ability("Void Healing", "hp_automatic"),
			ability("Keen Eyes", "special_sense"),
			ability("Twin Strike", "offensive"),
		},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sb := resp.Creature["stat_block"].(map[string]any)
	def := sb["defense"].(map[string]any)
	if got := namesOf(sb["interaction_abilities"]); !reflect.DeepEqual(got, []string{"Independent Brains"}) {
		t.Errorf("interaction_abilities = %v", got)
	}
	if got := namesOf(def["reactive_abilities"]); !reflect.DeepEqual(got, []string{"Head Bite"}) {
		t.Errorf("reactive_abilities = %v", got)
	}
	hp := def["hitpoints"].([]any)[0].(map[string]any)
	if got := namesOf(hp["automatic_abilities"]); !reflect.DeepEqual(got, []string{"Void Healing"}) {
		t.Errorf("hitpoints automatic_abilities = %v", got)
	}
	if got := namesOf(sb["senses"].(map[string]any)["special_senses"]); !reflect.DeepEqual(got, []string{"Keen Eyes"}) {
		t.Errorf("special_senses = %v", got)
	}
	if got := namesOf(sb["offense"].(map[string]any)["offensive_actions"]); !reflect.DeepEqual(got, []string{"Twin Strike"}) {
		t.Errorf("offensive_actions = %v", got)
	}
	if len(resp.PatchDoc.AppliedPatches) == 0 {
		t.Error("expected patches from the synthesized change")
	}
}

func TestAddItems_DedupAgainstExisting(t *testing.T) {
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Changes: []Change{{
			ChangeCategory: "abilities",
			Abilities:      []any{ability("Ferocity", "automatic")},
			Effects: []Effect{{
				Operation: "add_items",
				Source:    "$.changes[*].abilities[?(@.name=='Ferocity')]",
				Target:    "$.defense.automatic_abilities",
			}},
		}},
	}}
	sb := baseStatBlock()
	sb["defense"].(map[string]any)["automatic_abilities"] = []any{ability("Ferocity", "automatic")}
	creature := map[string]any{"stat_block": sb}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got := namesOf(resp.Creature["stat_block"].(map[string]any)["defense"].(map[string]any)["automatic_abilities"])
	if !reflect.DeepEqual(got, []string{"Ferocity"}) {
		t.Errorf("expected dedup, got %v", got)
	}
}

func TestValueFromSpeedItemGetsNamed(t *testing.T) {
	// Amphibious/Winged shape: a speed item added via value_from must carry a
	// display name with the computed distance ("swim 15 feet", not "swim").
	sb := map[string]any{
		"offense": map[string]any{
			"speed": map[string]any{
				"movement": []any{
					map[string]any{"movement_type": "walk", "name": "25 feet", "value": float64(25),
						"subtype": "speed", "type": "stat_block_section"},
				},
			},
		},
	}
	eff := Effect{
		Operation:   "add_item",
		Target:      "$.offense.speed.movement",
		Conditional: "$.offense.speed.movement[?(@.movement_type=='swim')] == null",
		Item: map[string]any{"movement_type": "swim", "name": "swim",
			"subtype": "speed", "type": "stat_block_section"},
		ValueFrom: "$.offense.speed.movement[?(@.movement_type=='walk')].value / 2",
		Minimum:   floatPtr(15),
	}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
	mv := sb["offense"].(map[string]any)["speed"].(map[string]any)["movement"].([]any)
	added := mv[len(mv)-1].(map[string]any)
	if added["name"] != "swim 15 feet" || added["value"] != float64(15) {
		t.Errorf("added speed entry = %#v", added)
	}
}

func TestValueFromWalkSpeedNameIsBare(t *testing.T) {
	// Walk entries on parsed creatures are named "25 feet" with no prefix —
	// the amphibious walk-from-swim arm must match that.
	sb := map[string]any{
		"offense": map[string]any{
			"speed": map[string]any{
				"movement": []any{
					map[string]any{"movement_type": "swim", "name": "swim 60 feet", "value": float64(60),
						"subtype": "speed", "type": "stat_block_section"},
				},
			},
		},
	}
	eff := Effect{
		Operation:   "add_item",
		Target:      "$.offense.speed.movement",
		Conditional: "$.offense.speed.movement[?(@.movement_type=='walk')] == null",
		Item: map[string]any{"movement_type": "walk", "name": "walk",
			"subtype": "speed", "type": "stat_block_section"},
		ValueFrom: "$.offense.speed.movement[?(@.movement_type=='swim')].value / 2",
		Minimum:   floatPtr(15),
	}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
	mv := sb["offense"].(map[string]any)["speed"].(map[string]any)["movement"].([]any)
	added := mv[len(mv)-1].(map[string]any)
	if added["name"] != "30 feet" || added["value"] != float64(30) {
		t.Errorf("added walk entry = %#v", added)
	}
}

func floatPtr(f float64) *float64 { return &f }

func TestAddItems_CategoryMarkedSenseDivertsFromUnfiltered(t *testing.T) {
	// A sense marked by ability_category but with a name outside the known
	// list (Spiritsense-style) must divert like any sense on the unfiltered
	// path — category and name-list signals are unified.
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Abilities: []any{ability("Spiritsense", "special_sense")},
		Changes: []Change{{
			ChangeCategory: "abilities",
			Effects: []Effect{{
				Operation: "add_items",
				Source:    "$.changes[*].abilities",
				Target:    "$.defense.automatic_abilities",
			}},
		}},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sb := resp.Creature["stat_block"].(map[string]any)
	senses := namesOf(sb["senses"].(map[string]any)["special_senses"])
	if !reflect.DeepEqual(senses, []string{"Spiritsense"}) {
		t.Errorf("special_senses = %v", senses)
	}
	if auto := sb["defense"].(map[string]any)["automatic_abilities"]; auto != nil {
		t.Errorf("automatic_abilities should be empty, got %v", auto)
	}
}

func TestAddItems_NonSenseNotAddedToSenseTarget(t *testing.T) {
	// Unfiltered source with a special_senses target: non-sense abilities
	// are not added there (or anywhere) — pinned so the guard can't silently
	// become an append.
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Changes: []Change{{
			ChangeCategory: "abilities",
			Abilities: []any{
				ability("Darkvision", "special_sense"),
				ability("Frightful Moan", "offensive"),
			},
			Effects: []Effect{{
				Operation: "add_items",
				Source:    "$.changes[*].abilities",
				Target:    "$.senses.special_senses",
			}},
		}},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sb := resp.Creature["stat_block"].(map[string]any)
	senses := namesOf(sb["senses"].(map[string]any)["special_senses"])
	if !reflect.DeepEqual(senses, []string{"Darkvision"}) {
		t.Errorf("special_senses = %v", senses)
	}
	off := namesOf(sb["offense"].(map[string]any)["offensive_actions"])
	if len(off) != 0 {
		t.Errorf("offensive_actions should be empty, got %v", off)
	}
}

func TestAddItems_UnparseableFilterErrors(t *testing.T) {
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Changes: []Change{{
			ChangeCategory: "abilities",
			Abilities:      []any{ability("Darkvision", "special_sense")},
			Effects: []Effect{{
				Operation: "add_items",
				Source:    `$.changes[*].abilities[?(@.name=="Darkvision")]`,
				Target:    "$.senses.special_senses",
			}},
		}},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	if _, err := Apply(creature, tmpl); err == nil {
		t.Error("expected error for filter syntax the engine cannot parse")
	}
}

func TestAddItems_NoAliasingAcrossWildcardTargets(t *testing.T) {
	// hp_automatic abilities land on every hitpoints entry; the entries must
	// not share one map (multi-headed creatures get later in-place edits).
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Abilities: []any{ability("Void Healing", "hp_automatic")},
	}}
	sb := baseStatBlock()
	sb["defense"].(map[string]any)["hitpoints"] = []any{
		map[string]any{"hp": float64(30)},
		map[string]any{"hp": float64(30)},
	}
	creature := map[string]any{"stat_block": sb}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	hps := resp.Creature["stat_block"].(map[string]any)["defense"].(map[string]any)["hitpoints"].([]any)
	a0 := hps[0].(map[string]any)["automatic_abilities"].([]any)[0].(map[string]any)
	a1 := hps[1].(map[string]any)["automatic_abilities"].([]any)[0].(map[string]any)
	a0["name"] = "Mutated"
	if a1["name"] != "Void Healing" {
		t.Errorf("hitpoints entries share one ability map: %v", a1["name"])
	}
}

func TestTargetForAbility_ResidualArms(t *testing.T) {
	// String ability with a known sense name routes to special_senses;
	// map ability with no category and a non-sense name defaults to
	// automatic_abilities.
	if got := targetForAbility("darkvision"); got != "$.senses.special_senses" {
		t.Errorf("string sense -> %s", got)
	}
	if got := targetForAbility("mystery string"); got != "$.defense.automatic_abilities" {
		t.Errorf("string non-sense -> %s", got)
	}
	noCat := map[string]any{"name": "Slam Door", "type": "stat_block_section"}
	if got := targetForAbility(noCat); got != "$.defense.automatic_abilities" {
		t.Errorf("no-category non-sense -> %s", got)
	}
}

func TestEffectConsumption(t *testing.T) {
	cases := []struct {
		eff        Effect
		name       string
		all, sense bool
	}{
		{Effect{Item: ability("Howl", "offensive")}, "Howl", false, false},
		{Effect{Source: "$.changes[*].abilities[?(@.name=='Stalk')]"}, "Stalk", false, false},
		{Effect{Source: "$.changes[*].abilities", Target: "$.senses.special_senses"}, "", false, true},
		{Effect{Source: "$.changes[*].abilities", Target: "$.defense.automatic_abilities"}, "", true, false},
		{Effect{Source: `$.changes[*].abilities[?(@.name=="bad quotes")]`}, "", false, false},
		{Effect{}, "", false, false},
	}
	for i, c := range cases {
		name, all, sense := effectConsumption(c.eff)
		if name != c.name || all != c.all || sense != c.sense {
			t.Errorf("case %d: got (%q,%v,%v) want (%q,%v,%v)", i, name, all, sense, c.name, c.all, c.sense)
		}
	}
}

func TestAddItem_CreatesMissingTargetArray(t *testing.T) {
	// add_item on an absent container must create it (the create-missing
	// branch in applySingleEffect, post add_items interception).
	sb := map[string]any{"offense": map[string]any{"speed": map[string]any{}}}
	eff := Effect{
		Operation: "add_item",
		Target:    "$.offense.speed.movement",
		Item: map[string]any{"movement_type": "swim", "name": "swim 15 feet",
			"subtype": "speed", "type": "stat_block_section", "value": float64(15)},
	}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
	mv := sb["offense"].(map[string]any)["speed"].(map[string]any)["movement"].([]any)
	if len(mv) != 1 || mv[0].(map[string]any)["name"] != "swim 15 feet" {
		t.Errorf("movement = %v", mv)
	}
}

func TestAddItems_CaseFoldDedupOnDivertedSense(t *testing.T) {
	// Templates carry "Darkvision"; parsed creatures store "darkvision".
	// The case-insensitive dedup is load-bearing on the diverted-sense path.
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Abilities: []any{ability("Darkvision", "special_sense")},
		Changes: []Change{{
			ChangeCategory: "abilities",
			Effects: []Effect{{
				Operation: "add_items",
				Source:    "$.changes[*].abilities",
				Target:    "$.defense.automatic_abilities",
			}},
		}},
	}}
	sb := baseStatBlock()
	sb["senses"] = map[string]any{"special_senses": []any{
		map[string]any{"name": "darkvision", "subtype": "ability", "type": "stat_block_section"},
	}}
	creature := map[string]any{"stat_block": sb}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	senses := namesOf(resp.Creature["stat_block"].(map[string]any)["senses"].(map[string]any)["special_senses"])
	if !reflect.DeepEqual(senses, []string{"darkvision"}) {
		t.Errorf("special_senses = %v (case-fold dedup failed)", senses)
	}
}

func TestMonsterTemplateAbilitiesJSONTag(t *testing.T) {
	// The abilities field's JSON boundary must survive without fixtures —
	// fixture tests skip in CI, so this is the only tag guard CI sees.
	raw := `{"name":"Tengu","monster_template":{"name":"Tengu","changes":[],
		"abilities":[{"name":"Low-Light Vision","ability_category":"special_sense",
		"type":"stat_block_section"}]}}`
	var tmpl TemplateJSON
	if err := json.Unmarshal([]byte(raw), &tmpl); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(tmpl.MonsterTemplate.Abilities) != 1 {
		t.Fatalf("abilities not decoded: %+v", tmpl.MonsterTemplate)
	}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	senses := namesOf(resp.Creature["stat_block"].(map[string]any)["senses"].(map[string]any)["special_senses"])
	if !reflect.DeepEqual(senses, []string{"Low-Light Vision"}) {
		t.Errorf("special_senses = %v", senses)
	}
}

func TestAddItems_UnresolvableTargetErrors(t *testing.T) {
	// A target that neither resolves nor can be created (zombie's old
	// "$.stat_block.interaction_abilities" shape) must error, not silently
	// drop the ability.
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Changes: []Change{{
			ChangeCategory: "abilities",
			Abilities:      []any{ability("Slow", "automatic")},
			Effects: []Effect{{
				Operation: "add_items",
				Source:    "$.changes[*].abilities[?(@.name=='Slow')]",
				Target:    "$.stat_block.interaction_abilities",
			}},
		}},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	if _, err := Apply(creature, tmpl); err == nil {
		t.Error("expected error for unresolvable add_items target")
	}
}

func TestAddItems_EmptySourceErrors(t *testing.T) {
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Changes: []Change{{
			ChangeCategory: "abilities",
			Abilities:      []any{ability("Orphan", "automatic")},
			Effects: []Effect{{
				Operation: "add_items",
				Target:    "$.defense.automatic_abilities",
			}},
		}},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	if _, err := Apply(creature, tmpl); err == nil {
		t.Error("expected error for add_items with neither item nor source")
	}
}

func TestApplyChange_NonAddItemsErrorPropagates(t *testing.T) {
	// The non-add_items propagation arm: a replace with an empty target
	// surfaces resolvePath's error through applyChange and Apply.
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Changes: []Change{{
			ChangeCategory: "defense",
			Effects:        []Effect{{Operation: "replace", Target: "", Value: float64(1)}},
		}},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	if _, err := Apply(creature, tmpl); err == nil {
		t.Error("expected resolve error to propagate for non-add_items effect")
	}
}

func TestSetMovementName_NonNumericComputedNoOp(t *testing.T) {
	item := map[string]any{"movement_type": "swim", "name": "swim"}
	setMovementName(item, "not a number")
	if item["name"] != "swim" {
		t.Errorf("name should be untouched for non-numeric computed, got %v", item["name"])
	}
}

func TestAddItems_CategorylessKnownSenseDiverts(t *testing.T) {
	// isSenseAbility's name-list fallback: a category-less Darkvision on the
	// unfiltered path must still divert to special_senses.
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Changes: []Change{{
			ChangeCategory: "abilities",
			Abilities: []any{map[string]any{
				"name": "Darkvision", "type": "stat_block_section"}},
			Effects: []Effect{{
				Operation: "add_items",
				Source:    "$.changes[*].abilities",
				Target:    "$.defense.automatic_abilities",
			}},
		}},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	sb := resp.Creature["stat_block"].(map[string]any)
	senses := namesOf(sb["senses"].(map[string]any)["special_senses"])
	if !reflect.DeepEqual(senses, []string{"Darkvision"}) {
		t.Errorf("special_senses = %v", senses)
	}
	if auto := sb["defense"].(map[string]any)["automatic_abilities"]; auto != nil {
		t.Errorf("automatic_abilities should be empty, got %v", auto)
	}
}

func TestAppendItemsAt_NonArrayTargetCoerced(t *testing.T) {
	sb := map[string]any{"defense": map[string]any{"automatic_abilities": "oops"}}
	if err := appendItemsAt(sb, "$.defense.automatic_abilities",
		[]any{ability("Ferocity", "automatic")}); err != nil {
		t.Fatalf("appendItemsAt: %v", err)
	}
	if err := appendItemsAt(sb, "", []any{ability("X", "automatic")}); err == nil {
		t.Error("expected resolve error for empty target path")
	}
	got := namesOf(sb["defense"].(map[string]any)["automatic_abilities"])
	if !reflect.DeepEqual(got, []string{"Ferocity"}) {
		t.Errorf("automatic_abilities = %v", got)
	}
}
