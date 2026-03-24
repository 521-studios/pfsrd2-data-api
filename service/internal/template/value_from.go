package template

import (
	"fmt"
	"math"
	"strings"
)

// evaluateValueFrom resolves a value_from expression against the stat_block.
// Supported forms:
//
//	$.path[*].field | max          — maximum of all resolved values
//	$.path[*].field | min          — minimum of all resolved values
//	$.path[?(@.x=='y')].field / 2 — single value divided by 2
//	$.path | high_for_level        — lookup from building rules table
func evaluateValueFrom(statBlock map[string]any, expr string, minimum *float64) (any, error) {
	expr = strings.TrimSpace(expr)

	// Split on pipe or division operator
	if idx := strings.LastIndex(expr, " | "); idx >= 0 {
		path := strings.TrimSpace(expr[:idx])
		op := strings.TrimSpace(expr[idx+3:])
		return evaluateAggregateOp(statBlock, path, op)
	}

	if idx := strings.LastIndex(expr, " / "); idx >= 0 {
		path := strings.TrimSpace(expr[:idx])
		divisorStr := strings.TrimSpace(expr[idx+3:])
		divisor, ok := toFloat64(parseRHS(divisorStr))
		if !ok {
			return nil, fmt.Errorf("invalid divisor in value_from: %s", divisorStr)
		}
		val, err := resolveSingleNumeric(statBlock, path)
		if err != nil {
			return nil, err
		}
		result := math.Floor(val / divisor)
		if minimum != nil && result < *minimum {
			result = *minimum
		}
		return result, nil
	}

	// Plain path — resolve single value
	val, ok := resolveScalar(statBlock, expr)
	if !ok {
		return nil, fmt.Errorf("value_from path not found: %s", expr)
	}
	return val, nil
}

func evaluateAggregateOp(statBlock map[string]any, path, op string) (any, error) {
	switch op {
	case "max":
		return resolveAggregate(statBlock, path, math.Max)
	case "min":
		return resolveAggregate(statBlock, path, math.Min)
	case "high_for_level":
		return resolveHighForLevel(statBlock, path)
	default:
		return nil, fmt.Errorf("unknown value_from operator: %s", op)
	}
}

func resolveAggregate(statBlock map[string]any, path string, fn func(float64, float64) float64) (float64, error) {
	resolved, err := resolvePath(statBlock, path)
	if err != nil {
		return 0, fmt.Errorf("resolve %q: %w", path, err)
	}
	if len(resolved) == 0 {
		return 0, fmt.Errorf("no values at path %q", path)
	}

	first := true
	var result float64
	for _, rv := range resolved {
		v, ok := toFloat64(rv.Get())
		if !ok {
			continue
		}
		if first {
			result = v
			first = false
		} else {
			result = fn(result, v)
		}
	}
	if first {
		return 0, fmt.Errorf("no numeric values at path %q", path)
	}
	return result, nil
}

func resolveSingleNumeric(statBlock map[string]any, path string) (float64, error) {
	resolved, err := resolvePath(statBlock, path)
	if err != nil {
		return 0, fmt.Errorf("resolve %q: %w", path, err)
	}
	if len(resolved) == 0 {
		return 0, fmt.Errorf("no value at path %q", path)
	}
	v, ok := toFloat64(resolved[0].Get())
	if !ok {
		return 0, fmt.Errorf("non-numeric value at path %q", path)
	}
	return v, nil
}

// resolveHighForLevel looks up the "high" skill value from the PF2e building
// rules table based on the creature's level.
func resolveHighForLevel(statBlock map[string]any, path string) (float64, error) {
	level, ok := resolveScalar(statBlock, "$.creature_type.level")
	if !ok {
		return 0, fmt.Errorf("cannot determine creature level for high_for_level")
	}
	lvl, ok := toFloat64(level)
	if !ok {
		return 0, fmt.Errorf("creature level is not numeric")
	}

	// PF2e building rules: high skill modifier by level
	// From Table 2-12: Monster Skill Modifiers (Game Mastery Guide / Monster Core)
	highSkill := map[int]float64{
		-1: 8, 0: 9, 1: 10, 2: 11, 3: 13, 4: 15, 5: 16,
		6: 18, 7: 20, 8: 21, 9: 23, 10: 25, 11: 26, 12: 28,
		13: 30, 14: 31, 15: 33, 16: 35, 17: 36, 18: 38,
		19: 40, 20: 41, 21: 43, 22: 45, 23: 46, 24: 48,
	}

	v, ok := highSkill[int(lvl)]
	if !ok {
		// Extrapolate for out-of-range levels
		if lvl < -1 {
			v = 8
		} else {
			v = 48
		}
	}
	return v, nil
}
