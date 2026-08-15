package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/521studios/pfsrd2-data-api/internal/db"
	"github.com/521studios/pfsrd2-data-api/internal/eligibility"
)

func TestGetEntryEligible(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()
	d := db.Global()

	insert := func(gameID, typ, name, attrs string) {
		if _, err := d.Exec(`INSERT INTO entries(game_id, type, name, current_schema_version,
			base_path, s3_key, edition, attrs) VALUES(?,?,?,?,?,?,?,?)`,
			gameID, typ, name, "1.3", typ+"/b/x.json", "json/"+typ+"/1.3/b/x.json", "remastered", attrs); err != nil {
			t.Fatalf("insert %s: %v", gameID, err)
		}
	}

	// A melee piercing weapon (the item under test).
	insert("wrapier", "weapons", "Rapier",
		`{"weapon_types":["Melee"],"damage_types":["piercing"],"weapon_category":"Martial"}`)
	// Fundamental potency (no clauses) → eligible, grouped under fundamental.
	insert("rpotency", "equipment", "Weapon Potency",
		`{"rune_form":"fundamental","rune_slot":"weapon_potency","rune_host":"weapon",
		  "rune_grades":[{"level":2,"price":"35 gp","grants_property_slots":1}]}`)
	// Keen: property, requires piercing/slashing AND melee → eligible.
	insert("rkeen", "equipment", "Keen",
		`{"rune_form":"property","rune_slot":"property","rune_host":"weapon",
		  "rune_requires":[{"op":"in","path":"$…damage_type","values":["piercing","slashing"]},
		                   {"op":"in","path":"$…weapon_type","values":["Melee"]}],
		  "rune_grades":[{"level":13,"price":"3,000 gp"}]}`)
	// Shifting: property, requires Ranged → NOT eligible on a melee weapon.
	insert("rshift", "equipment", "Shifting",
		`{"rune_form":"property","rune_slot":"property","rune_host":"weapon",
		  "rune_requires":[{"op":"in","path":"$…weapon_type","values":["Ranged"]}]}`)
	// Armor Potency: wrong host → excluded by the coarse filter.
	insert("rarmor", "equipment", "Armor Potency",
		`{"rune_form":"fundamental","rune_slot":"armor_potency","rune_host":"armor"}`)
	// A weapon material use-page.
	insert("mdawn", "equipment", "Dawnsilver Weapon",
		`{"material_use_host":"weapon","material_precious":true,
		  "material_grades":[{"grade":"standard","max_rune_level":15},{"grade":"high"}],
		  "material_grants_traits":["Rare"]}`)

	req := httptest.NewRequest("GET", "/api/pfsrd2/entries/wrapier/eligible", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code %d: %s", w.Code, w.Body)
	}

	var resp eligibility.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Item.Host != "weapon" || resp.Item.Name != "Rapier" {
		t.Fatalf("item = %+v", resp.Item)
	}
	if len(resp.Runes.Fundamental) != 1 || resp.Runes.Fundamental[0].Name != "Weapon Potency" {
		t.Fatalf("fundamental = %+v", resp.Runes.Fundamental)
	}
	if len(resp.Runes.Property) != 1 || resp.Runes.Property[0].Name != "Keen" {
		t.Fatalf("property = %+v (want only Keen; Shifting/Armor excluded)", resp.Runes.Property)
	}
	// Rich response: grades inlined with price, no follow-up fetch.
	if g := resp.Runes.Property[0].Grades; len(g) != 1 || g[0].Price != "3,000 gp" {
		t.Fatalf("keen grades = %+v", g)
	}
	if len(resp.Materials) != 1 || resp.Materials[0].Name != "Dawnsilver Weapon" || !resp.Materials[0].Precious {
		t.Fatalf("materials = %+v", resp.Materials)
	}
	if resp.Spells != nil {
		t.Errorf("a weapon has no spell slots: %+v", resp.Spells)
	}
}

func TestGetEntryEligible_NotFound(t *testing.T) {
	setupTestDB(t)
	r := newTestRouter()
	req := httptest.NewRequest("GET", "/api/pfsrd2/entries/nope/eligible", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("code %d, want 404", w.Code)
	}
}
