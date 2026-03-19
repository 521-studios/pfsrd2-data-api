package template

import (
	"encoding/json"
	"fmt"

	"github.com/wI2L/jsondiff"
)

// Apply applies a monster template to a creature stat block, returning grouped
// RFC 6902 patches and the modified result.
//
// The creature parameter should be the full creature JSON (with stat_block).
// The tmpl parameter is the parsed template JSON.
func Apply(creature map[string]any, tmpl TemplateJSON) (*ApplyResult, error) {
	working := deepCopy(creature).(map[string]any)

	statBlock, ok := working["stat_block"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("creature has no stat_block")
	}

	var patches []PatchGroup

	for _, change := range tmpl.MonsterTemplate.Changes {
		// Snapshot before this change category
		beforeBytes, err := json.Marshal(working)
		if err != nil {
			return nil, fmt.Errorf("marshal before snapshot: %w", err)
		}

		if err := applyChange(statBlock, change); err != nil {
			return nil, fmt.Errorf("apply change %q: %w", change.ChangeCategory, err)
		}

		// Diff to produce RFC 6902 operations
		afterBytes, err := json.Marshal(working)
		if err != nil {
			return nil, fmt.Errorf("marshal after snapshot: %w", err)
		}

		patch, err := jsondiff.CompareJSON(beforeBytes, afterBytes)
		if err != nil {
			return nil, fmt.Errorf("diff change %q: %w", change.ChangeCategory, err)
		}

		if len(patch) > 0 {
			ops := make([]Operation, len(patch))
			for i, op := range patch {
				ops[i] = Operation{
					Op:    op.Type,
					Path:  op.Path,
					Value: op.Value,
				}
			}
			patches = append(patches, PatchGroup{
				ChangeCategory: change.ChangeCategory,
				Description:    change.Text,
				Operations:     ops,
			})
		}
	}

	return &ApplyResult{
		PatchDoc: PatchDocument{
			AppliedPatches: patches,
			Selections:     []any{},
		},
		Creature: working,
	}, nil
}

// applyChange applies all effects within a single Change to the stat_block.
func applyChange(statBlock map[string]any, change Change) error {
	// Group effects by target to identify conditional chains.
	// Effects sharing a target form a first-match chain.
	type targetGroup struct {
		target  string
		effects []Effect
	}

	var groups []targetGroup
	seen := map[string]int{} // target → index in groups

	for _, eff := range change.Effects {
		if idx, ok := seen[eff.Target]; ok {
			groups[idx].effects = append(groups[idx].effects, eff)
		} else {
			seen[eff.Target] = len(groups)
			groups = append(groups, targetGroup{target: eff.Target, effects: []Effect{eff}})
		}
	}

	for _, g := range groups {
		if err := applyEffectGroup(statBlock, g.effects); err != nil {
			return err
		}
	}
	return nil
}

// applyEffectGroup applies a group of effects sharing the same target.
// Within a group, conditionals form a first-match chain.
func applyEffectGroup(statBlock map[string]any, effects []Effect) error {
	if len(effects) == 0 {
		return nil
	}

	target := effects[0].Target
	hasWildcard := containsWildcard(target)

	if hasWildcard {
		return applyWildcardEffects(statBlock, effects)
	}

	// Non-wildcard: evaluate conditional chain, first match wins
	for _, eff := range effects {
		if evaluateConditional(statBlock, eff.Conditional, -1) {
			return applySingleEffect(statBlock, eff, -1)
			// First match wins — skip rest
		}
	}
	return nil
}

// applyWildcardEffects handles effects with [*] targets. For damage effects,
// the conditional is evaluated per-element (correlated wildcards).
func applyWildcardEffects(statBlock map[string]any, effects []Effect) error {
	target := effects[0].Target

	// Resolve the target to get all matching locations
	resolved, err := resolvePath(statBlock, target)
	if err != nil {
		return err
	}

	for _, rv := range resolved {
		// For each resolved location, evaluate the conditional chain
		matched := false
		for _, eff := range effects {
			if evaluateConditional(statBlock, eff.Conditional, rv.ArrayIndex) {
				if err := applyOperation(rv, eff); err != nil {
					return err
				}
				matched = true
				break // first match wins
			}
		}
		_ = matched
	}
	return nil
}

// applySingleEffect resolves the target and applies the operation.
func applySingleEffect(statBlock map[string]any, eff Effect, arrayIdx int) error {
	resolved, err := resolvePath(statBlock, eff.Target)
	if err != nil {
		return err
	}
	for _, rv := range resolved {
		if err := applyOperation(rv, eff); err != nil {
			return err
		}
	}
	return nil
}

// applyOperation performs the actual mutation on a resolved value.
func applyOperation(rv ResolvedValue, eff Effect) error {
	switch eff.Operation {
	case "adjustment":
		return applyAdjustment(rv, eff)
	case "add_modifier":
		return applyAddModifier(rv, eff)
	default:
		return fmt.Errorf("unsupported operation: %s", eff.Operation)
	}
}

// applyAdjustment adds a numeric value to the resolved target.
// For arrays of numbers (like bonuses [29, 24, 19]), it adds to each element.
func applyAdjustment(rv ResolvedValue, eff Effect) error {
	adj, ok := toFloat64(eff.Value)
	if !ok {
		return fmt.Errorf("adjustment value is not numeric: %v", eff.Value)
	}

	current := rv.Get()
	switch val := current.(type) {
	case float64:
		rv.Set(val + adj)
	case []any:
		// Array of numbers (e.g., bonuses [29, 24, 19])
		for i, elem := range val {
			if num, ok := toFloat64(elem); ok {
				val[i] = num + adj
			}
		}
	default:
		return fmt.Errorf("cannot adjust non-numeric value: %T", current)
	}
	return nil
}

// applyAddModifier appends a modifier object to the target array.
func applyAddModifier(rv ResolvedValue, eff Effect) error {
	current := rv.Get()
	arr, ok := current.([]any)
	if !ok {
		// If target is nil or not an array, create a new array
		arr = []any{}
	}

	// Convert modifier map to any for JSON compatibility
	modifier := make(map[string]any)
	for k, v := range eff.Modifier {
		modifier[k] = v
	}

	arr = append(arr, modifier)
	rv.Set(arr)
	return nil
}

func containsWildcard(path string) bool {
	return len(path) > 0 && (path[0] == '$' || true) && // always check
		len(path) > 2 && containsStr(path, "[*]")
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
