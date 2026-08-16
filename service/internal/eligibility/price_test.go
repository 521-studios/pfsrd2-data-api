package eligibility

import (
	"encoding/json"
	"testing"
)

func TestPriceToCp(t *testing.T) {
	cp := func(n int) *int { return &n }
	cases := []struct {
		in   string
		want *int
	}{
		{"65 gp", cp(6500)},
		{"1,065 gp", cp(106500)},
		{"70,000 gp", cp(7000000)},
		{"5 sp", cp(50)},
		{"3 cp", cp(3)},
		{"", nil},
		{"Varies", nil},    // not a plain amount
		{"2 gold", nil},    // unknown unit
		{"free gp", nil},   // non-numeric amount
		{"1 gp 5 sp", nil}, // compound amounts aren't in the corpus; leave absent
	}
	for _, c := range cases {
		got := priceToCp(c.in)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("priceToCp(%q) = %v, want %v", c.in, deref(got), deref(c.want))
		}
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestBuildRunes_ParsesGradePriceToCp(t *testing.T) {
	lvl := func(n int) *int { return &n }
	rune := RuneInfo{Form: "fundamental", Slot: "striking", Host: "weapon", Grades: []Grade{
		{Level: lvl(4), Price: "65 gp"},
		{Level: lvl(12), Price: "1,065 gp"},
		{Level: lvl(19), Price: "Varies"}, // unparseable → PriceCp stays nil
	}}
	attrs, _ := json.Marshal(rune)
	f := ItemFacts{WeaponTypes: []string{"Melee"}, Host: "weapon"}
	g := BuildRunes([]Candidate{{GameID: "r1", Name: "Striking", Attrs: attrs}}, f)
	if len(g.Fundamental) != 1 {
		t.Fatalf("want 1 fundamental rune, got %d", len(g.Fundamental))
	}
	grades := g.Fundamental[0].Grades
	if grades[0].PriceCp == nil || *grades[0].PriceCp != 6500 {
		t.Errorf("grade[0].PriceCp = %v, want 6500", deref(grades[0].PriceCp))
	}
	if grades[1].PriceCp == nil || *grades[1].PriceCp != 106500 {
		t.Errorf("grade[1].PriceCp = %v, want 106500", deref(grades[1].PriceCp))
	}
	if grades[2].PriceCp != nil {
		t.Errorf("grade[2].PriceCp = %v, want nil (unparseable 'Varies')", deref(grades[2].PriceCp))
	}
	// The display string is preserved for rendering.
	if grades[0].Price != "65 gp" {
		t.Errorf("grade[0].Price = %q, want '65 gp'", grades[0].Price)
	}
}

// The index's price_cp is authoritative: when the parser emits rune_grades[].price_cp,
// BuildRunes must serve THAT value, not re-derive it from the display string. A grade
// whose indexed price_cp deliberately disagrees with priceToCp(Price) proves which one
// wins; a grade with no indexed price_cp falls back to parsing the string (back-compat).
func TestBuildRunes_PrefersIndexedPriceCp(t *testing.T) {
	lvl := func(n int) *int { return &n }
	cp := func(n int) *int { return &n }
	rune := RuneInfo{Form: "fundamental", Slot: "striking", Host: "weapon", Grades: []Grade{
		{Level: lvl(4), Price: "65 gp", PriceCp: cp(7000)}, // indexed 7000 ≠ priceToCp("65 gp")=6500
		{Level: lvl(12), Price: "1,065 gp"},                // no indexed value → fall back to 106500
	}}
	attrs, _ := json.Marshal(rune)
	f := ItemFacts{WeaponTypes: []string{"Melee"}, Host: "weapon"}
	g := BuildRunes([]Candidate{{GameID: "r1", Name: "Striking", Attrs: attrs}}, f)
	if len(g.Fundamental) != 1 {
		t.Fatalf("want 1 fundamental rune, got %d", len(g.Fundamental))
	}
	grades := g.Fundamental[0].Grades
	if grades[0].PriceCp == nil || *grades[0].PriceCp != 7000 {
		t.Errorf("grade[0].PriceCp = %v, want 7000 (indexed value, not re-derived 6500)", deref(grades[0].PriceCp))
	}
	if grades[1].PriceCp == nil || *grades[1].PriceCp != 106500 {
		t.Errorf("grade[1].PriceCp = %v, want 106500 (fallback string parse)", deref(grades[1].PriceCp))
	}
}
