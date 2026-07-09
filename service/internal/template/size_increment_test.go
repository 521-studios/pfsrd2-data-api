package template

import "testing"

func applySize(t *testing.T, size string, steps any) map[string]any {
	t.Helper()
	sb := map[string]any{
		"creature_type": map[string]any{
			"size": size,
			"traits": []any{
				map[string]any{"name": "Uncommon", "type": "trait"},
				map[string]any{"name": size, "type": "trait"},
			},
		},
	}
	eff := Effect{
		Operation: "size_increment",
		Target:    "$.creature_type.size",
		Value:     steps,
	}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
	return sb["creature_type"].(map[string]any)
}

func TestSizeIncrementSteps(t *testing.T) {
	cases := []struct {
		size  string
		steps any
		want  string
	}{
		{"Small", 1, "Medium"},
		{"Medium", 1, "Large"},
		{"Tiny", 2, "Medium"},
		{"Large", -1, "Medium"},
		{"Gargantuan", 1, "Gargantuan"}, // clamped at top
		{"Tiny", -1, "Tiny"},            // clamped at bottom
	}
	for _, c := range cases {
		ct := applySize(t, c.size, c.steps)
		if got := ct["size"]; got != c.want {
			t.Errorf("%s %+v: size = %v, want %s", c.size, c.steps, got, c.want)
		}
	}
}

func TestSizeIncrementRenamesTraitBadge(t *testing.T) {
	ct := applySize(t, "Small", 1)
	traits := ct["traits"].([]any)
	if name := traits[1].(map[string]any)["name"]; name != "Medium" {
		t.Errorf("size trait badge = %v, want Medium", name)
	}
	if name := traits[0].(map[string]any)["name"]; name != "Uncommon" {
		t.Errorf("unrelated trait mutated: %v", name)
	}
}

func TestSizeIncrementUnknownSize(t *testing.T) {
	sb := map[string]any{
		"creature_type": map[string]any{"size": "Colossal", "traits": []any{}},
	}
	eff := Effect{Operation: "size_increment", Target: "$.creature_type.size", Value: 1}
	if err := applyEffectGroup(sb, []Effect{eff}); err == nil {
		t.Fatal("expected error for unknown size")
	}
}

func TestSizeIncrementFractionalSteps(t *testing.T) {
	sb := map[string]any{
		"creature_type": map[string]any{"size": "Small", "traits": []any{}},
	}
	eff := Effect{Operation: "size_increment", Target: "$.creature_type.size", Value: 0.5}
	if err := applyEffectGroup(sb, []Effect{eff}); err == nil {
		t.Fatal("expected error for fractional steps")
	}
}
