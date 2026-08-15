// Package eligibility answers "what can I apply to this item?" — which runes,
// materials, and spell options are legal for a given weapon/armor/shield. It works
// entirely off the index attrs (populated by pfsrd2-automation's EquipmentExtractor):
// the item's five clause values plus each candidate's rune/material/spell fields, so
// no per-candidate JSON fetch is needed. Rune eligibility is the AND-of-ORs `requires`
// clauses plus the rules-that-aren't-fields (shields take only reinforcing, specific
// items take no property runes, staves take no property runes); needs_review runes are
// excluded. Pure + testable; the handler wires the DB queries.
package eligibility

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ItemFacts are the values a rune's requires clauses are evaluated against, plus the
// inputs to the derived rules. Sourced from the item entry's index attrs + type.
type ItemFacts struct {
	Host            string // weapon | armor | shield ("" = not a rune/material host)
	WeaponTypes     []string
	DamageTypes     []string
	Category        string // weapon or armor category (Light/Martial/…)
	Name            string
	Traits          []string
	ItemCategory    string
	ItemSubcategory string
}

// HostForType maps an index entry type to the rune/material host kind.
func HostForType(t string) string {
	switch t {
	case "weapons":
		return "weapon"
	case "armor":
		return "armor"
	case "shields":
		return "shield"
	default:
		return ""
	}
}

// itemAttrs is the subset of an item's index attrs we read.
type itemAttrs struct {
	WeaponTypes     []string `json:"weapon_types"`
	DamageTypes     []string `json:"damage_types"`
	WeaponCategory  string   `json:"weapon_category"`
	ArmorCategory   string   `json:"armor_category"`
	Traits          []string `json:"traits"`
	ItemCategory    string   `json:"item_category"`
	ItemSubcategory string   `json:"item_subcategory"`
}

// FactsFor builds the eligibility facts from an item's type, name and index attrs.
func FactsFor(itemType, name string, attrs json.RawMessage) (ItemFacts, error) {
	var a itemAttrs
	if len(attrs) > 0 {
		if err := json.Unmarshal(attrs, &a); err != nil {
			return ItemFacts{}, err
		}
	}
	cat := a.WeaponCategory
	if cat == "" {
		cat = a.ArmorCategory
	}
	return ItemFacts{
		Host:            HostForType(itemType),
		WeaponTypes:     a.WeaponTypes,
		DamageTypes:     a.DamageTypes,
		Category:        cat,
		Name:            name,
		Traits:          a.Traits,
		ItemCategory:    a.ItemCategory,
		ItemSubcategory: a.ItemSubcategory,
	}, nil
}

// Clause is one requires clause: op "in", a JSONPath, and the OR-set of values.
type Clause struct {
	Op     string   `json:"op"`
	Path   string   `json:"path"`
	Values []string `json:"values"`
}

// RuneInfo is the subset of a candidate rune's index attrs eligibility reads.
type RuneInfo struct {
	Form        string   `json:"rune_form"`
	Slot        string   `json:"rune_slot"`
	Host        string   `json:"rune_host"`
	NeedsReview bool     `json:"rune_needs_review"`
	Requires    []Clause `json:"rune_requires"`
	Grades      []Grade  `json:"rune_grades"`
}

// Grade is one graded variant of a rune.
type Grade struct {
	Level               *int   `json:"level,omitempty"`
	Price               string `json:"price,omitempty"`
	GrantsPropertySlots *int   `json:"grants_property_slots,omitempty"`
}

// clauseValues returns the item fact values a clause path resolves against. The
// corpus uses exactly five paths: four are matched by substring (so a tweak to the
// exact JSONPath text won't silently break them), and $.name by exact equality.
func clauseValues(path string, f ItemFacts) []string {
	switch {
	case strings.Contains(path, "weapon_type"):
		return f.WeaponTypes
	case strings.Contains(path, "damage_type"):
		return f.DamageTypes
	case strings.Contains(path, "category"):
		return oneOrNone(f.Category)
	case path == "$.name":
		return oneOrNone(f.Name)
	case strings.Contains(path, "traits"):
		return f.Traits
	}
	return nil
}

// clauseSatisfied is true when any of the item's values for the clause's path is in
// the clause's OR-set (an unknown path or a clause with no overlap is not satisfied).
func clauseSatisfied(c Clause, f ItemFacts) bool {
	vals := clauseValues(c.Path, f)
	for _, v := range vals {
		for _, want := range c.Values {
			if v == want {
				return true
			}
		}
	}
	return false
}

var specificSubcategories = map[string]bool{
	"Specific Magic Weapons": true,
	"Specific Magic Armor":   true,
	"Specific Shields":       true,
}

// RuneEligible reports whether a rune can be etched onto the item: host matches,
// every requires clause holds (AND of ORs), it isn't a needs_review (unconstrained-
// unknown) rune, and the derived rules allow it — shields take only the reinforcing
// slot; specific magic items and staves take no property runes.
func RuneEligible(r RuneInfo, f ItemFacts) bool {
	if f.Host == "" || r.Host != f.Host || r.NeedsReview {
		return false
	}
	if f.Host == "shield" && r.Slot != "reinforcing" {
		return false
	}
	if r.Form == "property" && (specificSubcategories[f.ItemSubcategory] || f.ItemCategory == "Staves") {
		return false
	}
	for _, c := range r.Requires {
		if !clauseSatisfied(c, f) {
			return false
		}
	}
	return true
}

func oneOrNone(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// MaterialEligible reports whether a material's use page fits the item — its host
// must match. The write side (item-apply) shares this with the read-side query so
// the two can't drift.
func MaterialEligible(materialAttrs json.RawMessage, f ItemFacts) bool {
	var m struct {
		UseHost string `json:"material_use_host"`
	}
	if err := json.Unmarshal(materialAttrs, &m); err != nil {
		return false
	}
	return f.Host != "" && m.UseHost == f.Host
}

// SpellFits reports (nil == fits) whether a spell may be slotted into a holder: the
// item must be a holder, the spell's rank must not exceed the holder's max_rank, and
// a cantrip is refused when the holder excludes cantrips. The reason is returned so
// the API can explain the refusal.
func SpellFits(holderAttrs json.RawMessage, spellRank int, isCantrip bool) error {
	var h struct {
		Holder   string   `json:"spell_holder"`
		MaxRank  any      `json:"spell_max_rank"`
		Excluded []string `json:"spell_excluded_types"`
	}
	if err := json.Unmarshal(holderAttrs, &h); err != nil {
		return err
	}
	if h.Holder == "" {
		return fmt.Errorf("this item is not a spell holder")
	}
	if isCantrip {
		for _, e := range h.Excluded {
			if strings.EqualFold(e, "cantrip") {
				return fmt.Errorf("this %s cannot hold cantrips", h.Holder)
			}
		}
		return nil // a cantrip has no rank to gate on
	}
	if maxRank, ok := h.MaxRank.(float64); ok && spellRank > int(maxRank) {
		return fmt.Errorf("spell rank %d exceeds this %s's maximum rank %d", spellRank, h.Holder, int(maxRank))
	}
	return nil
}
