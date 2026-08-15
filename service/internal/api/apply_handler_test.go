package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/521studios/pfsrd2-data-api/internal/db"
)

// A base weapon with a modifier-less strike, so the potency rune's add_modifier has
// to create the target array (the documented behaviour).
const rapierJSON = `{"name":"Rapier","stat_block":{"traits":[],"offense":{"weapon_modes":[
	{"weapon_type":"Melee","damage":[{"damage_type":"piercing","dice_count":1,"die_size":6,"formula":"1d6"}]}]}}}`

// Weapon Potency: fundamental, two grades; +1 grants an item attack bonus.
const potencyJSON = `{"name":"Weapon Potency","stat_block":{"variants":[
	{"level":2,"name":"Weapon Potency (+1)","effects":[{"operation":"add_modifier",
	 "target":"$.stat_block.offense.weapon_modes[*].modifiers",
	 "modifier":{"type":"bonus","subtype":"attack","bonus_type":"item","bonus_value":1}}]}]}}`

func applyMock() *mockS3 {
	return &mockS3{objects: map[string][]byte{
		"json/weapons/1.3/b/rapier.json":    []byte(rapierJSON),
		"json/equipment/1.3/b/potency.json": []byte(potencyJSON),
	}}
}

func insertEntry(t *testing.T, gameID, typ, name, s3key, edition, attrs string) {
	t.Helper()
	if _, err := db.Global().Exec(`INSERT INTO entries(game_id, type, name, current_schema_version,
		base_path, s3_key, edition, attrs) VALUES(?,?,?,?,?,?,?,?)`,
		gameID, typ, name, "1.3", typ+"/b/x.json", s3key, edition, attrs); err != nil {
		t.Fatalf("insert %s: %v", gameID, err)
	}
}

func TestApplyRuneToItem(t *testing.T) {
	setupTestDB(t)
	r := newTestRouterWithS3(applyMock())
	insertEntry(t, "rap", "weapons", "Rapier", "json/weapons/1.3/b/rapier.json", "remastered",
		`{"weapon_types":["Melee"],"damage_types":["piercing"],"weapon_category":"Martial"}`)
	insertEntry(t, "pot", "equipment", "Weapon Potency", "json/equipment/1.3/b/potency.json", "remastered",
		`{"rune_form":"fundamental","rune_slot":"weapon_potency","rune_host":"weapon"}`)

	req := httptest.NewRequest("GET", "/api/pfsrd2/entries/rap/apply/pot?grade=2", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code %d: %s", w.Code, w.Body)
	}
	// Uniform JSON contract {item, applied, patches} for every effect kind.
	body := w.Body.String()
	if !strings.Contains(body, `"applied":"Weapon Potency (+1)"`) {
		t.Fatalf("missing applied label: %s", body)
	}
	// The engine emitted an add op that puts an item attack modifier on the strike.
	if !strings.Contains(body, `"add"`) || !strings.Contains(body, `"attack"`) || !strings.Contains(body, "modifiers") {
		t.Fatalf("expected an add-modifier patch, got: %s", body)
	}
}

func TestApplyRune_NoMatchingGrade422(t *testing.T) {
	setupTestDB(t)
	r := newTestRouterWithS3(applyMock())
	insertEntry(t, "rap", "weapons", "Rapier", "json/weapons/1.3/b/rapier.json", "remastered",
		`{"weapon_types":["Melee"],"damage_types":["piercing"],"weapon_category":"Martial"}`)
	insertEntry(t, "pot", "equipment", "Weapon Potency", "json/equipment/1.3/b/potency.json", "remastered",
		`{"rune_form":"fundamental","rune_slot":"weapon_potency","rune_host":"weapon"}`)
	// potencyJSON only has a level-2 grade; asking for grade 7 must not silently
	// downgrade — it's a 422.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/pfsrd2/entries/rap/apply/pot?grade=7", nil))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code %d, want 422 for a non-existent grade", w.Code)
	}
}

func TestApply_S3Error(t *testing.T) {
	setupTestDB(t)
	r := newTestRouterWithS3(&errS3{}) // every S3 fetch fails
	insertEntry(t, "rap", "weapons", "Rapier", "json/weapons/1.3/b/rapier.json", "remastered",
		`{"weapon_types":["Melee"]}`)
	insertEntry(t, "pot", "equipment", "Weapon Potency", "json/equipment/1.3/b/potency.json", "remastered",
		`{"rune_form":"fundamental","rune_slot":"weapon_potency","rune_host":"weapon"}`)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/pfsrd2/entries/rap/apply/pot", nil))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("code %d, want 502 on an S3 fetch failure", w.Code)
	}
}

func TestApply_NotFoundAndUnknownKind(t *testing.T) {
	setupTestDB(t)
	r := newTestRouterWithS3(applyMock())
	insertEntry(t, "rap", "weapons", "Rapier", "json/weapons/1.3/b/rapier.json", "remastered",
		`{"weapon_types":["Melee"]}`)

	// A missing effect game_id → 404.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/pfsrd2/entries/rap/apply/nope", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing effect: code %d, want 404", w.Code)
	}

	// A plain equipment entry (not a rune/material/spell) → 400.
	insertEntry(t, "plain", "equipment", "Backpack", "json/equipment/1.3/b/x.json", "remastered",
		`{"item_category":"Adventuring Gear"}`)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/pfsrd2/entries/rap/apply/plain", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown effect kind: code %d, want 400", w.Code)
	}
}

func TestApplyRune_BoundaryRefusesIneligible(t *testing.T) {
	setupTestDB(t)
	r := newTestRouterWithS3(applyMock())
	insertEntry(t, "rap", "weapons", "Rapier", "json/weapons/1.3/b/rapier.json", "remastered",
		`{"weapon_types":["Melee"],"damage_types":["piercing"]}`)
	// An armor rune — not eligible for a weapon; the API must refuse it.
	insertEntry(t, "arm", "equipment", "Armor Potency", "json/equipment/1.3/b/potency.json", "remastered",
		`{"rune_form":"fundamental","rune_slot":"armor_potency","rune_host":"armor"}`)

	req := httptest.NewRequest("GET", "/api/pfsrd2/entries/rap/apply/arm", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("code %d, want 409 (ineligible rune refused)", w.Code)
	}
}

// The POST form applies to the item in the body, so a customize panel can stack
// several modifications: each apply chains onto the previous result, not the S3 base.
func TestApplyToItemPost_ChainsOnBodyItem(t *testing.T) {
	setupTestDB(t)
	r := newTestRouterWithS3(applyMock())
	insertEntry(t, "rap", "weapons", "Rapier", "json/weapons/1.3/b/rapier.json", "remastered",
		`{"weapon_types":["Melee"],"damage_types":["piercing"],"weapon_category":"Martial"}`)
	insertEntry(t, "pot", "equipment", "Weapon Potency", "json/equipment/1.3/b/potency.json", "remastered",
		`{"rune_form":"fundamental","rune_slot":"weapon_potency","rune_host":"weapon"}`)

	// An in-progress item: renamed + already Striking'd (dice_count 2). Potency must
	// land on THIS, leaving the custom name and the earlier dice bump intact.
	body := `{"name":"Custom Blade","stat_block":{"traits":[],"offense":{"weapon_modes":[
		{"weapon_type":"Melee","damage":[{"damage_type":"piercing","dice_count":2,"die_size":6,"formula":"2d6"}]}]}}}`
	req := httptest.NewRequest("POST", "/api/pfsrd2/entries/rap/apply/pot?grade=2", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code %d: %s", w.Code, w.Body)
	}
	var res struct {
		Item    map[string]any `json:"item"`
		Applied string         `json:"applied"`
	}
	json.Unmarshal(w.Body.Bytes(), &res)
	if res.Item["name"] != "Custom Blade" {
		t.Fatalf("name = %v, want the body item preserved (not the S3 base rapier)", res.Item["name"])
	}
	dmg := res.Item["stat_block"].(map[string]any)["offense"].(map[string]any)["weapon_modes"].([]any)[0].(map[string]any)["damage"].([]any)[0].(map[string]any)
	if dmg["dice_count"] != float64(2) {
		t.Fatalf("dice_count = %v, want 2 (the earlier Striking preserved)", dmg["dice_count"])
	}
	if !strings.Contains(w.Body.String(), `"attack"`) {
		t.Fatalf("potency not applied to the body item: %s", w.Body)
	}
}

func TestApplyToItemPost_InvalidBody400(t *testing.T) {
	setupTestDB(t)
	r := newTestRouterWithS3(applyMock())
	insertEntry(t, "rap", "weapons", "Rapier", "json/weapons/1.3/b/rapier.json", "remastered", `{"weapon_types":["Melee"]}`)
	insertEntry(t, "pot", "equipment", "Weapon Potency", "json/equipment/1.3/b/potency.json", "remastered",
		`{"rune_form":"fundamental","rune_slot":"weapon_potency","rune_host":"weapon"}`)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/pfsrd2/entries/rap/apply/pot", strings.NewReader("{ not json")))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code %d, want 400 on a malformed item body", w.Code)
	}
}

func TestApplyToItemPost_NullBody400(t *testing.T) {
	setupTestDB(t)
	r := newTestRouterWithS3(applyMock())
	insertEntry(t, "rap", "weapons", "Rapier", "json/weapons/1.3/b/rapier.json", "remastered", `{"weapon_types":["Melee"]}`)
	insertEntry(t, "pot", "equipment", "Weapon Potency", "json/equipment/1.3/b/potency.json", "remastered",
		`{"rune_form":"fundamental","rune_slot":"weapon_potency","rune_host":"weapon"}`)
	// Valid JSON `null` decodes to a nil map — the "item body required" guard, distinct
	// from the malformed-JSON branch above.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/pfsrd2/entries/rap/apply/pot", strings.NewReader("null")))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code %d, want 400 on a null item body", w.Code)
	}
}

func TestApplyToItemPost_BoundaryStillEnforced(t *testing.T) {
	setupTestDB(t)
	r := newTestRouterWithS3(applyMock())
	insertEntry(t, "rap", "weapons", "Rapier", "json/weapons/1.3/b/rapier.json", "remastered", `{"weapon_types":["Melee"]}`)
	insertEntry(t, "arm", "equipment", "Armor Potency", "json/equipment/1.3/b/potency.json", "remastered",
		`{"rune_form":"fundamental","rune_slot":"armor_potency","rune_host":"armor"}`)
	body := `{"name":"Custom Blade","stat_block":{"traits":[],"offense":{"weapon_modes":[{"weapon_type":"Melee"}]}}}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/pfsrd2/entries/rap/apply/arm", strings.NewReader(body)))
	if w.Code != http.StatusConflict {
		t.Fatalf("code %d, want 409 (armor rune on a weapon, via POST)", w.Code)
	}
}

func TestApplyMaterialAndSpell(t *testing.T) {
	setupTestDB(t)
	mock := &mockS3{objects: map[string][]byte{
		"json/weapons/1.3/b/rapier.json": []byte(rapierJSON),
		"json/equipment/1.3/b/wand.json": []byte(`{"name":"Magic Wand","stat_block":{"spell_slots":{"holder":"wand"}}}`),
	}}
	r := newTestRouterWithS3(mock)
	insertEntry(t, "rap", "weapons", "Rapier", "json/weapons/1.3/b/rapier.json", "remastered",
		`{"weapon_types":["Melee"],"weapon_category":"Martial"}`)
	insertEntry(t, "mat", "equipment", "Dawnsilver Weapon", "json/equipment/1.3/b/x.json", "remastered",
		`{"material_use_host":"weapon","material_grants_traits":["Rare"]}`)

	// Material apply → 200 with the item now carrying the granted rarity.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/pfsrd2/entries/rap/apply/mat", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("material apply %d: %s", w.Code, w.Body)
	}
	var mres struct {
		Applied string         `json:"applied"`
		Item    map[string]any `json:"item"`
	}
	json.Unmarshal(w.Body.Bytes(), &mres)
	if mres.Applied != "Dawnsilver Weapon" {
		t.Fatalf("applied = %q", mres.Applied)
	}

	// Spell apply on a wand → 200; over-rank → 409.
	insertEntry(t, "wand", "equipment", "Magic Wand", "json/equipment/1.3/b/wand.json", "remastered",
		`{"spell_holder":"wand","spell_max_rank":9,"spell_excluded_types":["cantrip"]}`)
	lvl5 := `5`
	insertSpell(t, "fireball", "Fireball", lvl5, `{"traits":["Fire"]}`)
	insertSpell(t, "wish", "Wish", `10`, `{"traits":["Concentrate"]}`)

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/pfsrd2/entries/wand/apply/fireball", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("spell apply %d: %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/pfsrd2/entries/wand/apply/wish", nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("over-rank spell %d, want 409", w.Code)
	}

	// A cantrip (rank 0, Cantrip trait) exercises spellRankAndCantrip's derivation
	// loop and the wand's cantrip exclusion → 409.
	insertSpell(t, "light", "Light", `0`, `{"traits":["Cantrip","Concentrate"]}`)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/pfsrd2/entries/wand/apply/light", nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("cantrip on a cantrip-excluding wand %d, want 409", w.Code)
	}
}

func insertSpell(t *testing.T, gameID, name, level, attrs string) {
	t.Helper()
	if _, err := db.Global().Exec(`INSERT INTO entries(game_id, type, name, current_schema_version,
		base_path, s3_key, level, edition, attrs) VALUES(?,?,?,?,?,?,?,?,?)`,
		gameID, "spells", name, "1.3", "spells/b/x.json", "json/spells/1.3/b/x.json", level, "remastered", attrs); err != nil {
		t.Fatalf("insert spell %s: %v", gameID, err)
	}
}
