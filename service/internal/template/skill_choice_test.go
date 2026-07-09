package template

import (
	"errors"
	"strings"
	"testing"
)

func skillChoiceTmpl() TemplateJSON {
	return TemplateJSON{
		MonsterTemplate: MonsterTemplate{
			Changes: []Change{{
				ChangeCategory: "abilities",
				Text:           "- Choose a skill. The creature gains the official bully ability for that skill.",
				Effects: []Effect{{
					Operation: "select",
					Target:    "$.interaction_abilities",
					Selection: map[string]any{
						"type": "select", "action": "choose_skill", "min": 1, "max": 1,
						"description": "Choose an Intelligence-, Wisdom-, or Charisma-based skill.",
						"options": []any{map[string]any{
							"name": "Official Bully",
							"effects": []any{map[string]any{
								"operation": "add_items",
								"source":    "$.abilities[?(@.name=='Official Bully')]",
								"target":    "$.interaction_abilities",
							}},
						}},
					},
				}},
			}},
			Abilities: []any{map[string]any{
				"name": "Official Bully", "subtype": "ability", "type": "stat_block_section",
				"ability_category": "interaction",
				"text":             "The creature can use the chosen skill to Coerce or Demoralize in place of Intimidation.",
			}},
		},
	}
}

func TestSkillChoiceTemplatesAbility(t *testing.T) {
	creature := map[string]any{"stat_block": map[string]any{"interaction_abilities": []any{}}}
	res, err := ApplyWithSelections(creature, skillChoiceTmpl(), []SelectionChoice{
		{ID: "c0/e0", Skill: "Legal Lore"},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	ia := res.Creature["stat_block"].(map[string]any)["interaction_abilities"].([]any)
	if len(ia) != 1 {
		t.Fatalf("expected 1 interaction ability, got %d", len(ia))
	}
	a := ia[0].(map[string]any)
	if a["name"] != "Official Bully (Legal Lore)" {
		t.Errorf("name = %v", a["name"])
	}
	text, _ := a["text"].(string)
	if !strings.Contains(text, "use Legal Lore to Coerce") {
		t.Errorf("skill not templated into text: %q", text)
	}
	if len(res.PatchDoc.AppliedSelections) != 1 {
		t.Errorf("applied_selections = %v", res.PatchDoc.AppliedSelections)
	}
}

func TestSkillChoiceRejections(t *testing.T) {
	creature := map[string]any{"stat_block": map[string]any{"interaction_abilities": []any{}}}
	cases := []struct {
		name  string
		skill string
	}{
		{"bad charset", "Legal Lore; DROP TABLE"},
		{"empty after regex", "1337"},
	}
	for _, c := range cases {
		_, err := ApplyWithSelections(creature, skillChoiceTmpl(), []SelectionChoice{{ID: "c0/e0", Skill: c.skill}})
		if err == nil || !errors.Is(err, ErrBadSelection) {
			t.Errorf("%s: expected ErrBadSelection, got %v", c.name, err)
		}
	}
	// skill on a non-choose_skill selection
	tmpl := skillChoiceTmpl()
	tmpl.MonsterTemplate.Changes[0].Effects[0].Selection["action"] = "replace"
	_, err := ApplyWithSelections(creature, tmpl, []SelectionChoice{{ID: "c0/e0", Skill: "Legal Lore"}})
	if err == nil || !strings.Contains(err.Error(), "does not take a skill") {
		t.Errorf("expected action rejection, got %v", err)
	}
}

func TestSkillChoicePoolUntouchedForOthers(t *testing.T) {
	// The templated pool is per-choice: an unanswered apply keeps the
	// original pool text (no leakage between requests).
	creature := map[string]any{"stat_block": map[string]any{"interaction_abilities": []any{}}}
	tmpl := skillChoiceTmpl()
	if _, err := ApplyWithSelections(creature, tmpl, []SelectionChoice{{ID: "c0/e0", Skill: "Genealogy Lore"}}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	orig := tmpl.MonsterTemplate.Abilities[0].(map[string]any)
	if !strings.Contains(orig["text"].(string), "the chosen skill") {
		t.Errorf("template pool mutated: %v", orig["text"])
	}
}
