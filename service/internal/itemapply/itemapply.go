// Package itemapply applies a rune, material, or spell to an item and returns the
// modified item — the write side of "what can I apply to this?". Rune effects reuse
// the template engine (they share its effect vocabulary); materials and spells are
// non-engine state changes. Every apply is boundary-checked through the eligibility
// package (RuneEligible / MaterialEligible / SpellFits) — the same predicates as the
// read side, so the two can't drift and the API is the single authority: an ineligible
// apply is refused, not silently performed. Pure/testable; the handler wires the DB +
// S3 fetches.
package itemapply

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/521studios/pfsrd2-data-api/internal/eligibility"
	"github.com/521studios/pfsrd2-data-api/internal/template"
)

// ErrIneligible wraps a boundary refusal (the effect can't legally apply to the
// item) so the handler maps it to 409 Conflict, distinct from a 500 data-integrity
// error like a malformed document.
var ErrIneligible = errors.New("ineligible")

// Kind classifies the thing being applied, derived from the effect entry.
type Kind int

const (
	KindUnknown Kind = iota
	KindRune
	KindMaterial
	KindSpell
)

// KindOf reports what an effect entry is: a rune (equipment with rune_host), a
// material (equipment with a material block), or a spell (type "spells").
func KindOf(effectType string, attrs json.RawMessage) Kind {
	if effectType == "spells" {
		return KindSpell
	}
	var probe struct {
		RuneHost         string          `json:"rune_host"`
		MaterialUseHost  string          `json:"material_use_host"`
		MaterialPrecious *bool           `json:"material_precious"`
		MaterialGrades   json.RawMessage `json:"material_grades"`
	}
	_ = json.Unmarshal(attrs, &probe)
	switch {
	case probe.RuneHost != "":
		return KindRune
	case probe.MaterialUseHost != "" || probe.MaterialPrecious != nil || len(probe.MaterialGrades) > 0:
		return KindMaterial
	}
	return KindUnknown
}

// normalizeTarget rewrites a rune effect's target from doc-relative
// ("$.stat_block.offense…") to the stat_block-relative form the engine expects
// ("$.offense…"); monster-template effects already use the short form.
func normalizeTarget(t string) string {
	if strings.HasPrefix(t, "$.stat_block.") {
		return "$." + strings.TrimPrefix(t, "$.stat_block.")
	}
	return t
}

// RuneVariantEffects returns the effects for the requested grade level of a rune,
// with targets normalized for the engine. grade<=0 selects the single/first grade.
// The rune doc is the full item JSON (map with stat_block.variants[].effects, or
// stat_block.effects for an ungraded rune).
func RuneVariantEffects(runeDoc map[string]any, grade int) ([]template.Effect, string, error) {
	sb, _ := runeDoc["stat_block"].(map[string]any)
	if sb == nil {
		return nil, "", fmt.Errorf("rune has no stat_block")
	}
	rawEffects, label, err := selectVariantEffects(sb, grade)
	if err != nil {
		return nil, "", err
	}
	if rawEffects == nil {
		return nil, "", fmt.Errorf("this rune grade carries no effects (property runes carry their mechanics as prose)")
	}
	effs, err := decodeEffects(rawEffects)
	if err != nil {
		return nil, "", err
	}
	for i := range effs {
		effs[i].Target = normalizeTarget(effs[i].Target)
	}
	return effs, label, nil
}

// selectVariantEffects picks a graded rune's variant. grade<=0 means "unspecified"
// and takes the first (lowest) grade; an explicit grade that matches no variant is an
// error rather than a silent downgrade.
func selectVariantEffects(sb map[string]any, grade int) (any, string, error) {
	variants, _ := sb["variants"].([]any)
	if len(variants) == 0 {
		return sb["effects"], "", nil // ungraded rune
	}
	if grade > 0 {
		for _, v := range variants {
			if vm, ok := v.(map[string]any); ok {
				if lvl, ok := vm["level"].(float64); ok && int(lvl) == grade {
					label, _ := vm["name"].(string)
					return vm["effects"], label, nil
				}
			}
		}
		return nil, "", fmt.Errorf("this rune has no grade at level %d", grade)
	}
	first, _ := variants[0].(map[string]any) // unspecified → first (lowest) grade
	label, _ := first["name"].(string)
	return first["effects"], label, nil
}

func decodeEffects(raw any) ([]template.Effect, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var effs []template.Effect
	if err := json.Unmarshal(b, &effs); err != nil {
		return nil, err
	}
	return effs, nil
}

// AsTemplate wraps rune effects as a single-change TemplateJSON so they run through
// the existing engine (template.Apply) and produce the same RFC 6902 patch document.
func AsTemplate(effects []template.Effect, label string) template.TemplateJSON {
	if label == "" {
		label = "rune"
	}
	return template.TemplateJSON{
		Name: label,
		MonsterTemplate: template.MonsterTemplate{
			Changes: []template.Change{{
				Text:           label,
				ChangeCategory: "rune",
				Effects:        effects,
			}},
		},
	}
}

// EnsureModifierTargets pre-creates the leaf array each add_modifier effect targets,
// so the engine appends to it rather than no-op'ing on a missing target. This is how
// a weapon potency rune grants an attack bonus to a base weapon whose strikes carry
// no modifier list (weapon_modes[*].modifiers is absent). The engine deliberately
// won't fabricate a missing target for monster templates (it shouldn't invent stats),
// so item-apply seeds the target here instead. Only add_modifier targets are touched;
// intermediate objects are never created (only the leaf array).
func EnsureModifierTargets(itemDoc map[string]any, effects []template.Effect) {
	sb, ok := itemDoc["stat_block"].(map[string]any)
	if !ok {
		return
	}
	for _, e := range effects {
		if e.Operation != "add_modifier" {
			continue
		}
		ensureLeafArray(sb, splitTarget(e.Target))
	}
}

// splitTarget strips the "$." root and splits a stat_block-relative path into
// segments (a "[*]" wildcard stays attached to its segment).
func splitTarget(target string) []string {
	t := strings.TrimPrefix(target, "$.")
	if t == "" {
		return nil
	}
	return strings.Split(t, ".")
}

func ensureLeafArray(node any, segs []string) {
	if len(segs) == 0 {
		return
	}
	seg, rest := segs[0], segs[1:]
	if key, wild := strings.CutSuffix(seg, "[*]"); wild {
		m, ok := node.(map[string]any)
		if !ok {
			return
		}
		arr, ok := m[key].([]any)
		if !ok {
			return
		}
		for _, el := range arr {
			ensureLeafArray(el, rest)
		}
		return
	}
	m, ok := node.(map[string]any)
	if !ok {
		return
	}
	if len(rest) == 0 { // leaf: create the array if absent
		if _, exists := m[seg]; !exists {
			m[seg] = []any{}
		}
		return
	}
	if child, exists := m[seg]; exists { // never fabricate intermediate objects
		ensureLeafArray(child, rest)
	}
}

// CheckRuneBoundary refuses an ineligible rune apply (the API is the authority):
// the same predicate the eligibility read side uses.
func CheckRuneBoundary(runeAttrs json.RawMessage, facts eligibility.ItemFacts) error {
	var ri eligibility.RuneInfo
	if err := json.Unmarshal(runeAttrs, &ri); err != nil {
		return err
	}
	if !eligibility.RuneEligible(ri, facts) {
		return fmt.Errorf("%w: this rune is not eligible for this item", ErrIneligible)
	}
	return nil
}
