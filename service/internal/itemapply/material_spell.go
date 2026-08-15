package itemapply

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/521studios/pfsrd2-data-api/internal/eligibility"
)

// rarityRank orders PF2 rarities; an item has exactly one, so a material's rarity
// doesn't union in — the more restrictive of the two wins.
var rarityRank = map[string]int{"common": 0, "uncommon": 1, "rare": 2, "unique": 3}

func isRarity(name string) bool {
	_, ok := rarityRank[strings.ToLower(name)]
	return ok
}

// ApplyMaterial makes the item out of a precious material: it gains the material's
// granted traits (except `precious`, which classifies the material itself), and its
// rarity becomes the more restrictive of its own and the material's. Boundary
// (eligibility.MaterialEligible): the material must have a use page for this host.
// Mutates itemDoc in place.
func ApplyMaterial(itemDoc map[string]any, materialAttrs json.RawMessage, facts eligibility.ItemFacts) error {
	if !eligibility.MaterialEligible(materialAttrs, facts) {
		return fmt.Errorf("%w: this material cannot be made into this item", ErrIneligible)
	}
	var m struct {
		GrantsTraits []string `json:"material_grants_traits"`
	}
	if err := json.Unmarshal(materialAttrs, &m); err != nil {
		return err
	}
	sb, ok := itemDoc["stat_block"].(map[string]any)
	if !ok {
		return fmt.Errorf("item has no stat_block")
	}
	traits, _ := sb["traits"].([]any)

	// Separate the item's current rarity (if any) from its other traits.
	present := map[string]bool{}
	itemRarity, hadRarity := "common", false
	kept := traits[:0:0]
	for _, t := range traits {
		tm, ok := t.(map[string]any)
		if !ok {
			kept = append(kept, t)
			continue
		}
		name, _ := tm["name"].(string)
		if isRarity(name) {
			itemRarity, hadRarity = strings.ToLower(name), true
			continue // drop the existing rarity; the winner is re-added below
		}
		present[strings.ToLower(name)] = true
		kept = append(kept, t)
	}

	// Granted non-rarity traits union in; the granted rarity competes for the max.
	winner, grantedRarity := itemRarity, false
	for _, gt := range m.GrantsTraits {
		if strings.EqualFold(gt, "precious") {
			continue
		}
		if isRarity(gt) {
			grantedRarity = true
			if rarityRank[strings.ToLower(gt)] > rarityRank[winner] {
				winner = strings.ToLower(gt)
			}
			continue
		}
		if !present[strings.ToLower(gt)] {
			kept = append(kept, map[string]any{"name": gt})
			present[strings.ToLower(gt)] = true
		}
	}
	// Only carry a rarity trait if the item already had one or the material granted
	// one — never fabricate a "Common" trait on an item that had no explicit rarity.
	if hadRarity || grantedRarity {
		kept = append(kept, map[string]any{"name": capitalize(winner)})
	}
	sb["traits"] = kept
	return nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ApplySpell writes a chosen spell into a holder's slot after the eligibility
// boundary (eligibility.SpellFits) passes: holder, rank, and cantrip-exclusion.
// Mutates holderDoc in place.
func ApplySpell(holderDoc map[string]any, holderAttrs json.RawMessage, spellName string, spellAonID, spellRank int, spellIsCantrip bool) error {
	if err := eligibility.SpellFits(holderAttrs, spellRank, spellIsCantrip); err != nil {
		return fmt.Errorf("%w: %s", ErrIneligible, err)
	}
	sb, ok := holderDoc["stat_block"].(map[string]any)
	if !ok {
		return fmt.Errorf("holder has no stat_block")
	}
	ss, ok := sb["spell_slots"].(map[string]any)
	if !ok {
		return fmt.Errorf("holder has no spell_slots")
	}
	spell := map[string]any{"name": spellName, "rank": spellRank}
	if spellAonID != 0 {
		spell["aonid"] = spellAonID
	}
	ss["spell"] = spell
	return nil
}
