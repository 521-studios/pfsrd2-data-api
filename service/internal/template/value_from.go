package template

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// ErrValueFromPath marks value_from failures caused by the creature lacking
// the referenced data (a swim-only creature has no walk speed). Callers skip
// these silently; every unmarked value_from error is a malformed template
// expression and must propagate.
var ErrValueFromPath = errors.New("value_from path unavailable")

// ErrValueFromShape marks failures where the path resolved but the values
// have the wrong shape (non-numeric where a number is required). These are
// template/schema mismatches, not per-creature gaps — callers skip them but
// warn loudly. Known producers (the attack.damage | min expressions in
// dwarf/kholo/ratfolk/vampire) are tracked for data fixes; once the corpus
// is clean this class should become a hard error.
var ErrValueFromShape = errors.New("value_from values have wrong shape")

// evaluateValueFrom resolves a value_from expression against the stat_block.
// Supported forms:
//
//	$.path[*].field | max          — maximum of all resolved values
//	$.path[*].field | min          — minimum of all resolved values
//	$.path[*].field | max / 2      — aggregate, then divide (5-foot floor)
//	$.path[?(@.x=='y')].field / 2 — single value divided by 2 (5-foot floor)
//	$.path | high_for_level        — lookup from building rules table
//
// The minimum applies to every numeric result. Divisions floor to the
// nearest 5 feet — the PF2 halved-Speed convention, and both division
// consumers are speeds.
func evaluateValueFrom(statBlock map[string]any, expr string, minimum *float64) (any, error) {
	expr = strings.TrimSpace(expr)

	// Split on pipe or division operator
	if idx := strings.LastIndex(expr, " | "); idx >= 0 {
		path := strings.TrimSpace(expr[:idx])
		op := strings.TrimSpace(expr[idx+3:])
		// "max / 2" — aggregate then divide (sandbound: half the fastest speed)
		if opIdx := strings.Index(op, " / "); opIdx >= 0 {
			divisor, ok := toFloat64(parseRHS(op[opIdx+3:]))
			if !ok || divisor == 0 {
				return nil, fmt.Errorf("invalid divisor in value_from: %s", op)
			}
			val, err := evaluateAggregateOp(statBlock, path, strings.TrimSpace(op[:opIdx]))
			if err != nil {
				return nil, err
			}
			num, ok := toFloat64(val)
			if !ok {
				return nil, fmt.Errorf("aggregate result not numeric in value_from: %s", expr)
			}
			return applyMinimum(divideToFive(num, divisor), minimum), nil
		}
		val, err := evaluateAggregateOp(statBlock, path, op)
		if err != nil {
			return nil, err
		}
		if num, ok := toFloat64(val); ok {
			return applyMinimum(num, minimum), nil
		}
		return val, nil
	}

	if idx := strings.LastIndex(expr, " / "); idx >= 0 {
		path := strings.TrimSpace(expr[:idx])
		divisorStr := strings.TrimSpace(expr[idx+3:])
		divisor, ok := toFloat64(parseRHS(divisorStr))
		if !ok || divisor == 0 {
			return nil, fmt.Errorf("invalid divisor in value_from: %s", divisorStr)
		}
		val, err := resolveSingleNumeric(statBlock, path)
		if err != nil {
			return nil, err
		}
		return applyMinimum(divideToFive(val, divisor), minimum), nil
	}

	// Plain path — resolve single value
	val, ok := resolveScalar(statBlock, expr)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrValueFromPath, expr)
	}
	if num, numOk := toFloat64(val); numOk {
		return applyMinimum(num, minimum), nil
	}
	return val, nil
}

// divideToFive divides and rounds down to the nearest 5 feet — the PF2
// halved-Speed convention (both division consumers are speeds).
func divideToFive(num, divisor float64) float64 {
	return math.Floor(num/divisor/5) * 5
}

// applyMinimum clamps a numeric result up to the effect's minimum.
func applyMinimum(v float64, minimum *float64) float64 {
	if minimum != nil && v < *minimum {
		return *minimum
	}
	return v
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
		return 0, fmt.Errorf("%w: no values at %q", ErrValueFromPath, path)
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
		return 0, fmt.Errorf("%w: no numeric values at %q", ErrValueFromShape, path)
	}
	return result, nil
}

func resolveSingleNumeric(statBlock map[string]any, path string) (float64, error) {
	resolved, err := resolvePath(statBlock, path)
	if err != nil {
		return 0, fmt.Errorf("resolve %q: %w", path, err)
	}
	if len(resolved) == 0 {
		return 0, fmt.Errorf("%w: no value at %q", ErrValueFromPath, path)
	}
	v, ok := toFloat64(resolved[0].Get())
	if !ok {
		return 0, fmt.Errorf("%w: non-numeric value at %q", ErrValueFromShape, path)
	}
	return v, nil
}

// resolveHighForLevel looks up the "high" skill value from the PF2e building
// rules table based on the creature's level. The path parameter is unused —
// high_for_level always derives from creature level.
func resolveHighForLevel(statBlock map[string]any, _ string) (float64, error) {
	level, ok := resolveScalar(statBlock, "$.creature_type.level")
	if !ok {
		return 0, fmt.Errorf("%w: no creature level for high_for_level", ErrValueFromPath)
	}
	lvl, ok := toFloat64(level)
	if !ok {
		return 0, fmt.Errorf("%w: creature level is not numeric (%T: %v)", ErrValueFromShape, level, level)
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
