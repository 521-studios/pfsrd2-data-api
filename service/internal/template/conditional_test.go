package template

import "testing"

func TestEvaluateConditional_Default(t *testing.T) {
	sb := map[string]any{}
	if !evaluateConditional(sb, "default", -1) {
		t.Error("default should be true")
	}
	if !evaluateConditional(sb, "", -1) {
		t.Error("empty should be true")
	}
}

func TestEvaluateConditional_LevelComparison(t *testing.T) {
	sb := map[string]any{
		"creature_type": map[string]any{"level": float64(14)},
	}

	tests := []struct {
		cond string
		want bool
	}{
		{"$.creature_type.level <= 0", false},
		{"$.creature_type.level >= 5 && $.creature_type.level <= 19", true},
		{"$.creature_type.level >= 20", false},
		{"$.creature_type.level == 14", true},
		{"$.creature_type.level > 13", true},
		{"$.creature_type.level < 15", true},
	}
	for _, tt := range tests {
		got := evaluateConditional(sb, tt.cond, -1)
		if got != tt.want {
			t.Errorf("evaluateConditional(%q) = %v, want %v", tt.cond, got, tt.want)
		}
	}
}

func TestEvaluateConditional_LevelZero(t *testing.T) {
	sb := map[string]any{
		"creature_type": map[string]any{"level": float64(0)},
	}
	if !evaluateConditional(sb, "$.creature_type.level <= 0", -1) {
		t.Error("level 0 should match <= 0")
	}
	if evaluateConditional(sb, "$.creature_type.level >= 2 && $.creature_type.level <= 4", -1) {
		t.Error("level 0 should not match >= 2 && <= 4")
	}
}

func TestEvaluateConditional_NullCheck(t *testing.T) {
	sb := map[string]any{
		"offense": map[string]any{
			"offensive_actions": []any{
				map[string]any{
					"attack": map[string]any{"weapon": "jaws"},
				},
				map[string]any{
					"ability": map[string]any{
						"frequency": "once per day",
					},
				},
			},
		},
	}

	// First element: no ability.frequency → should be null → != null is false
	got := evaluateConditional(sb, "$.offense.offensive_actions[*].ability.frequency != null", 0)
	if got {
		t.Error("element 0 has no ability.frequency, expected false for != null")
	}

	// Second element: has ability.frequency → != null is true
	got = evaluateConditional(sb, "$.offense.offensive_actions[*].ability.frequency != null", 1)
	if !got {
		t.Error("element 1 has ability.frequency, expected true for != null")
	}
}

func TestEvaluateConditional_WeakLevel1(t *testing.T) {
	sb := map[string]any{
		"creature_type": map[string]any{"level": float64(1)},
	}
	if !evaluateConditional(sb, "$.creature_type.level == 1", -1) {
		t.Error("level 1 should match == 1")
	}
}

func TestEvaluateConditional_AndOperator(t *testing.T) {
	sb := map[string]any{
		"creature_type": map[string]any{"level": float64(3)},
	}
	if !evaluateConditional(sb, "$.creature_type.level >= 2 && $.creature_type.level <= 4", -1) {
		t.Error("level 3 should match >= 2 && <= 4")
	}
	if evaluateConditional(sb, "$.creature_type.level >= 5 && $.creature_type.level <= 19", -1) {
		t.Error("level 3 should not match >= 5 && <= 19")
	}
}
