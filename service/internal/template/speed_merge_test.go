package template

import (
	"strconv"
	"testing"
)

func speedEntry(mt string, feet float64) map[string]any {
	return map[string]any{
		"movement_type": mt,
		"name":          mt + " " + strconv.Itoa(int(feet)) + " feet",
		"subtype":       "speed",
		"type":          "stat_block_section",
		"value":         feet,
	}
}

func applySpeedAdd(t *testing.T, existing []any, item map[string]any) []any {
	t.Helper()
	sb := map[string]any{
		"offense": map[string]any{
			"speed": map[string]any{"movement": existing},
		},
	}
	eff := Effect{
		Operation: "add_item",
		Target:    "$.offense.speed.movement",
		Item:      item,
	}
	if err := applyEffectGroup(sb, []Effect{eff}); err != nil {
		t.Fatalf("applyEffectGroup: %v", err)
	}
	speed := sb["offense"].(map[string]any)["speed"].(map[string]any)
	return speed["movement"].([]any)
}

func TestAddSpeedSlowerThanExistingIsNoop(t *testing.T) {
	// Athamaru's swim 25 onto Stingray's swim 30: leave it alone.
	out := applySpeedAdd(t, []any{speedEntry("swim", 30)}, speedEntry("swim", 25))
	if len(out) != 1 {
		t.Fatalf("expected 1 movement entry, got %d", len(out))
	}
	m := out[0].(map[string]any)
	if m["value"] != float64(30) || m["name"] != "swim 30 feet" {
		t.Errorf("existing faster swim modified: %v", m)
	}
}

func TestAddSpeedEqualToExistingIsNoop(t *testing.T) {
	out := applySpeedAdd(t, []any{speedEntry("swim", 25)}, speedEntry("swim", 25))
	if len(out) != 1 || out[0].(map[string]any)["value"] != float64(25) {
		t.Errorf("equal speed should be a no-op: %v", out)
	}
}

func TestAddSpeedFasterBumpsExisting(t *testing.T) {
	out := applySpeedAdd(t, []any{speedEntry("swim", 20)}, speedEntry("swim", 40))
	if len(out) != 1 {
		t.Fatalf("expected 1 movement entry, got %d", len(out))
	}
	m := out[0].(map[string]any)
	if m["value"] != float64(40) || m["name"] != "swim 40 feet" {
		t.Errorf("slower swim not bumped: %v", m)
	}
}

func TestAddSpeedNewTypeAppends(t *testing.T) {
	out := applySpeedAdd(t, []any{speedEntry("swim", 30)}, speedEntry("fly", 25))
	if len(out) != 2 {
		t.Fatalf("expected 2 movement entries, got %d", len(out))
	}
	if out[1].(map[string]any)["movement_type"] != "fly" {
		t.Errorf("fly speed not appended: %v", out)
	}
}

func TestAddSpeedExistingWithoutValueGetsBumped(t *testing.T) {
	existing := map[string]any{"movement_type": "swim", "name": "swim"}
	out := applySpeedAdd(t, []any{existing}, speedEntry("swim", 25))
	if len(out) != 1 {
		t.Fatalf("expected 1 movement entry, got %d", len(out))
	}
	m := out[0].(map[string]any)
	if m["value"] != float64(25) {
		t.Errorf("valueless existing speed not bumped: %v", m)
	}
}
