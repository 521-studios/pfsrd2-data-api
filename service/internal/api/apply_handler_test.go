package api

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
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
	patches := readPatchesPart(t, w)
	// The engine emitted an add op that puts an item attack modifier on the strike.
	if !strings.Contains(patches, `"add"`) || !strings.Contains(patches, `"attack"`) || !strings.Contains(patches, "modifiers") {
		t.Fatalf("expected an add-modifier patch, got: %s", patches)
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
}

func insertSpell(t *testing.T, gameID, name, level, attrs string) {
	t.Helper()
	if _, err := db.Global().Exec(`INSERT INTO entries(game_id, type, name, current_schema_version,
		base_path, s3_key, level, edition, attrs) VALUES(?,?,?,?,?,?,?,?,?)`,
		gameID, "spells", name, "1.3", "spells/b/x.json", "json/spells/1.3/b/x.json", level, "remastered", attrs); err != nil {
		t.Fatalf("insert spell %s: %v", gameID, err)
	}
}

func readPatchesPart(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	_, params, err := mime.ParseMediaType(w.Header().Get("Content-Type"))
	if err != nil {
		t.Fatalf("content-type: %v (%q)", err, w.Header().Get("Content-Type"))
	}
	mr := multipart.NewReader(w.Body, params["boundary"])
	part, err := mr.NextPart()
	if err != nil {
		t.Fatalf("first part: %v", err)
	}
	b, err := io.ReadAll(part)
	if err != nil {
		t.Fatalf("read part: %v", err)
	}
	return string(b)
}
