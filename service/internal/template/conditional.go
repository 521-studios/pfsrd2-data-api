package template

import (
	"strconv"
	"strings"
)

// evaluateConditional evaluates a conditional expression against the stat_block.
//
// Supported forms:
//
//	"default"                                          → always true
//	""                                                 → always true (no conditional)
//	"$.creature_type.level <= 0"                       → resolve path, compare
//	"$.X >= 2 && $.X <= 4"                             → split on &&, evaluate each
//	"$.offense.offensive_actions[*].ability.frequency != null" → null check (per-element)
//
// For per-element conditionals (paths containing [*]), arrayIdx specifies which
// array element to check. If arrayIdx < 0, the first resolved value is used.
func evaluateConditional(statBlock map[string]any, cond string, arrayIdx int) bool {
	cond = strings.TrimSpace(cond)
	if cond == "" || cond == "default" {
		return true
	}

	if strings.Contains(cond, "&&") {
		parts := strings.Split(cond, "&&")
		for _, part := range parts {
			if !evaluateSingleCondition(statBlock, strings.TrimSpace(part), arrayIdx) {
				return false
			}
		}
		return true
	}

	return evaluateSingleCondition(statBlock, cond, arrayIdx)
}

// evaluateSingleCondition handles one comparison: "$.path op value"
func evaluateSingleCondition(statBlock map[string]any, cond string, arrayIdx int) bool {
	// Try each operator
	for _, op := range []string{"!=", ">=", "<=", "==", ">", "<"} {
		idx := strings.Index(cond, " "+op+" ")
		if idx < 0 {
			continue
		}
		lhs := strings.TrimSpace(cond[:idx])
		rhs := strings.TrimSpace(cond[idx+len(op)+2:])
		return compareValues(statBlock, lhs, op, rhs, arrayIdx)
	}
	return false
}

func compareValues(statBlock map[string]any, lhsExpr, op, rhsExpr string, arrayIdx int) bool {
	lhsVal := resolveValueExpr(statBlock, lhsExpr, arrayIdx)

	// Handle null checks
	if rhsExpr == "null" {
		isNil := lhsVal == nil
		switch op {
		case "==":
			return isNil
		case "!=":
			return !isNil
		}
		return false
	}

	lhsNum, lhsOk := toFloat64(lhsVal)
	rhsNum, rhsOk := toFloat64(parseRHS(rhsExpr))
	if !lhsOk || !rhsOk {
		return false
	}

	switch op {
	case "==":
		return lhsNum == rhsNum
	case "!=":
		return lhsNum != rhsNum
	case ">=":
		return lhsNum >= rhsNum
	case "<=":
		return lhsNum <= rhsNum
	case ">":
		return lhsNum > rhsNum
	case "<":
		return lhsNum < rhsNum
	}
	return false
}

// resolveValueExpr resolves a value expression. If it starts with $., it's a
// path to resolve from the stat block. Otherwise it's a literal.
func resolveValueExpr(statBlock map[string]any, expr string, arrayIdx int) any {
	expr = strings.TrimSpace(expr)
	if !strings.HasPrefix(expr, "$.") {
		return parseRHS(expr)
	}

	// For paths with [*], we need to check a specific array element
	if strings.Contains(expr, "[*]") && arrayIdx >= 0 {
		return resolveAtIndex(statBlock, expr, arrayIdx)
	}

	val, _ := resolveScalar(statBlock, expr)
	return val
}

// resolveAtIndex resolves a path containing [*] at a specific array index.
// It replaces [*] with the specific index for evaluation.
func resolveAtIndex(statBlock map[string]any, path string, arrayIdx int) any {
	path = strings.TrimPrefix(path, "$.")
	segments := splitPath(path)

	var current any = statBlock
	for _, seg := range segments {
		if seg == "[*]" {
			arr, ok := current.([]any)
			if !ok || arrayIdx >= len(arr) {
				return nil
			}
			current = arr[arrayIdx]
			continue
		}
		m, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		val, exists := m[seg]
		if !exists {
			return nil
		}
		current = val
	}
	return current
}

func parseRHS(s string) any {
	s = strings.TrimSpace(s)
	if s == "null" {
		return nil
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return n
	}
	return s
}

func toFloat64(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	}
	return 0, false
}
