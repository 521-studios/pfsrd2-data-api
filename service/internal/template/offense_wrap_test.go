package template

import (
	"reflect"
	"strings"
	"testing"
)

func TestAddItems_OffensiveAbilityGetsWrapped(t *testing.T) {
	// Plain abilities appended to offensive_actions must take the wrapper
	// shape real creatures use ({name, ability, offensive_action_type}),
	// or the display layer cannot render them.
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Abilities: []any{ability("Drag Along", "offensive")},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	oa := resp.Creature["stat_block"].(map[string]any)["offense"].(map[string]any)["offensive_actions"].([]any)
	if len(oa) != 1 {
		t.Fatalf("offensive_actions = %v", oa)
	}
	w := oa[0].(map[string]any)
	if w["name"] != "Drag Along" || w["offensive_action_type"] != "ability" ||
		w["subtype"] != "offensive_action" || w["type"] != "stat_block_section" {
		t.Errorf("wrapper shape wrong: %#v", w)
	}
	inner, ok := w["ability"].(map[string]any)
	if !ok || inner["name"] != "Drag Along" {
		t.Errorf("inner ability wrong: %#v", w["ability"])
	}
}

func TestAddItems_AlreadyWrappedOffensiveEntryUntouched(t *testing.T) {
	// An item that already looks like a wrapper (has ability/attack/spells)
	// is appended as-is.
	wrapped := map[string]any{
		"name": "Prewrapped", "subtype": "offensive_action",
		"type": "stat_block_section", "offensive_action_type": "ability",
		"ability": map[string]any{"name": "Prewrapped", "type": "stat_block_section"},
	}
	tmpl := TemplateJSON{MonsterTemplate: MonsterTemplate{
		Changes: []Change{{
			ChangeCategory: "abilities",
			Abilities:      []any{wrapped},
			Effects: []Effect{{
				Operation: "add_items",
				Source:    "$.changes[*].abilities[?(@.name=='Prewrapped')]",
				Target:    "$.offense.offensive_actions",
			}},
		}},
	}}
	creature := map[string]any{"stat_block": baseStatBlock()}
	resp, err := Apply(creature, tmpl)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	oa := resp.Creature["stat_block"].(map[string]any)["offense"].(map[string]any)["offensive_actions"].([]any)
	got := oa[0].(map[string]any)
	if _, double := got["ability"].(map[string]any)["ability"]; double {
		t.Errorf("wrapper was wrapped again: %#v", got)
	}
}

func TestSetReach_ReducesReachTraitOnMeleeAttack(t *testing.T) {
	// Miniature semantics: a melee attack with a Reach trait has its value
	// reduced to the effect's value in feet; the trait text updates too.
	sb := map[string]any{
		"offense": map[string]any{
			"offensive_actions": []any{
				map[string]any{
					"name": "Melee",
					"attack": map[string]any{
						"attack_type": "melee",
						"name":        "Melee",
						"traits": []any{
							map[string]any{"name": "Reach", "value": "20 feet",
								"type": "stat_block_section"},
							map[string]any{"name": "Magical", "type": "stat_block_section"},
						},
					},
				},
				map[string]any{
					"name": "Melee",
					"attack": map[string]any{
						"attack_type": "melee",
						"name":        "Claw",
						"traits":      []any{},
					},
				},
			},
		},
	}
	eff := Effect{
		Operation: "set_reach",
		Target:    "$.offense.offensive_actions[*].attack",
		Value:     float64(5),
	}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
	oa := sb["offense"].(map[string]any)["offensive_actions"].([]any)
	traits := oa[0].(map[string]any)["attack"].(map[string]any)["traits"].([]any)
	reach := traits[0].(map[string]any)
	if reach["value"] != "5 feet" {
		t.Errorf("reach value = %v", reach["value"])
	}
	// No Reach trait: untouched, and no stray fields added.
	claw := oa[1].(map[string]any)["attack"].(map[string]any)
	if !reflect.DeepEqual(claw["traits"], []any{}) {
		t.Errorf("claw traits changed: %v", claw["traits"])
	}
	if _, has := claw["reach"]; has {
		t.Errorf("stray reach field added to claw")
	}
}

func TestSetReach_NonMapTargetNoOp(t *testing.T) {
	rv := ResolvedValue{}
	if err := applySetReach(rv, Effect{Operation: "set_reach", Value: float64(5)}); err != nil {
		t.Errorf("nil target should no-op, got %v", err)
	}
}

func TestSetReach_TextMentionUpdated(t *testing.T) {
	// If the Reach trait carries a display text, it follows the value.
	sb := map[string]any{
		"offense": map[string]any{
			"offensive_actions": []any{
				map[string]any{
					"name": "Melee",
					"attack": map[string]any{
						"attack_type": "melee",
						"traits": []any{
							map[string]any{"name": "Reach", "value": "15 feet",
								"text": "Reach 15 feet", "type": "stat_block_section"},
						},
					},
				},
			},
		},
	}
	eff := Effect{
		Operation: "set_reach",
		Target:    "$.offense.offensive_actions[*].attack",
		Value:     float64(5),
	}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
	tr := sb["offense"].(map[string]any)["offensive_actions"].([]any)[0].(map[string]any)["attack"].(map[string]any)["traits"].([]any)[0].(map[string]any)
	if tr["value"] != "5 feet" || !strings.Contains(tr["text"].(string), "5 feet") {
		t.Errorf("trait = %#v", tr)
	}
}
