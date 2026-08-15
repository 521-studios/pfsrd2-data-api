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
// rarity becomes the more restrictive of its own and the material's. Boundary: the
// material must have a use page for this item's host. Mutates itemDoc in place.
func ApplyMaterial(itemDoc map[string]any, materialAttrs json.RawMessage, facts eligibility.ItemFacts) error {
	var m struct {
		UseHost      string   `json:"material_use_host"`
		GrantsTraits []string `json:"material_grants_traits"`
	}
	if err := json.Unmarshal(materialAttrs, &m); err != nil {
		return err
	}
	if m.UseHost == "" || m.UseHost != facts.Host {
		return fmt.Errorf("this material cannot be made into this item")
	}
	sb, ok := itemDoc["stat_block"].(map[string]any)
	if !ok {
		return fmt.Errorf("item has no stat_block")
	}
	traits, _ := sb["traits"].([]any)

	// Current rarity + the set of trait names already present.
	present := map[string]bool{}
	itemRarity := "common"
	kept := traits[:0:0]
	for _, t := range traits {
		tm, ok := t.(map[string]any)
		if !ok {
			kept = append(kept, t)
			continue
		}
		name, _ := tm["name"].(string)
		if isRarity(name) {
			itemRarity = strings.ToLower(name)
			continue // drop existing rarity; re-added below as the winner
		}
		present[strings.ToLower(name)] = true
		kept = append(kept, t)
	}

	// Granted non-rarity traits union in; the granted rarity competes for the max.
	winnerRarity := itemRarity
	for _, gt := range m.GrantsTraits {
		if strings.EqualFold(gt, "precious") {
			continue
		}
		if isRarity(gt) {
			if rarityRank[strings.ToLower(gt)] > rarityRank[winnerRarity] {
				winnerRarity = strings.ToLower(gt)
			}
			continue
		}
		if !present[strings.ToLower(gt)] {
			kept = append(kept, map[string]any{"name": gt})
			present[strings.ToLower(gt)] = true
		}
	}
	kept = append(kept, map[string]any{"name": capitalize(winnerRarity)})
	sb["traits"] = kept
	return nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ApplySpell writes a chosen spell into a holder's slot. Boundaries: the item must
// be a holder; the spell's rank must not exceed the holder's max_rank; and a cantrip
// is refused when the holder excludes cantrips. Mutates holderDoc in place.
func ApplySpell(holderDoc map[string]any, holderAttrs json.RawMessage, spellName string, spellAonID, spellRank int, spellIsCantrip bool) error {
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
	maxRank, cantripOK := holderMaxRank(h.MaxRank)
	if spellIsCantrip {
		if !cantripOK || excludes(h.Excluded, "cantrip") {
			return fmt.Errorf("this %s cannot hold cantrips", h.Holder)
		}
	} else if maxRank >= 0 && spellRank > maxRank {
		return fmt.Errorf("spell rank %d exceeds this %s's maximum rank %d", spellRank, h.Holder, maxRank)
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

// holderMaxRank parses the holder's max_rank, which is an int or the string
// "cantrip" (a cantrip-only holder). Returns (rank, cantripAllowed).
func holderMaxRank(v any) (int, bool) {
	switch r := v.(type) {
	case float64:
		return int(r), true
	case string:
		if strings.EqualFold(r, "cantrip") {
			return 0, true
		}
	}
	return -1, true // unspecified → don't gate on rank
}

func excludes(list []string, want string) bool {
	for _, s := range list {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}
