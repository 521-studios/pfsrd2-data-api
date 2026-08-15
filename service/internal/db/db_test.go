package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// testDB creates an in-memory SQLite database with schema and fixture data.
// Returns the db and a cleanup function.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Schema — mirrors pfsrd2-automation/pfsrd2/indexer/schema.py
	ddl := `
		CREATE TABLE entries (
			id                     INTEGER PRIMARY KEY,
			game_id                TEXT UNIQUE NOT NULL,
			aonid                  INTEGER,
			type                   TEXT NOT NULL,
			name                   TEXT NOT NULL,
			current_schema_version TEXT NOT NULL,
			base_path              TEXT NOT NULL,
			s3_key                 TEXT NOT NULL,
			level                  INTEGER,
			source                 TEXT,
			source_page            INTEGER,
			edition                TEXT,
			image_s3_key           TEXT,
			attrs                  TEXT NOT NULL DEFAULT '{}',
			search_text            TEXT NOT NULL DEFAULT '',
			indexed_at             TEXT DEFAULT (datetime('now'))
		);
		CREATE TABLE entry_versions (
			id             INTEGER PRIMARY KEY,
			game_id        TEXT NOT NULL REFERENCES entries(game_id),
			schema_version TEXT NOT NULL,
			s3_key         TEXT NOT NULL,
			UNIQUE(game_id, schema_version)
		);
		CREATE TABLE sources (
			id    INTEGER PRIMARY KEY,
			name  TEXT UNIQUE NOT NULL,
			aonid INTEGER
		);
		CREATE VIRTUAL TABLE entries_fts USING fts5(
			name, source, search_text,
			content=entries, content_rowid=id,
			tokenize='unicode61 remove_diacritics 1'
		);
		CREATE VIRTUAL TABLE entries_trigram USING fts5(
			name, content=entries, content_rowid=id,
			tokenize='trigram'
		);
		CREATE TABLE alternates (
			game_id TEXT NOT NULL, alternate_game_id TEXT NOT NULL,
			alternate_type TEXT NOT NULL, PRIMARY KEY (game_id, alternate_game_id)
		);
	`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("schema: %v", err)
	}

	seedFixtures(t, db)
	return db
}

func seedFixtures(t *testing.T, db *sql.DB) {
	t.Helper()

	entries := []struct {
		gameID, typ, name, schema, basePath, s3Key string
		level                                      int
		source, edition                            string
		imagePath                                  *string
	}{
		{"Monsters:100", "monsters", "Adult Red Dragon", "1.3", "monsters/bestiary/adult_red_dragon.json", "json/monsters/1.3/bestiary/adult_red_dragon.json", 14, "Bestiary", "remastered", nil},
		{"Monsters:101", "monsters", "Pseudodragon", "1.3", "monsters/bestiary/pseudodragon.json", "json/monsters/1.3/bestiary/pseudodragon.json", 2, "Bestiary", "remastered", strPtr("images/Monsters/Pseudodragon.webp")},
		{"Monsters:102", "monsters", "Young Blue Dragon", "1.3", "monsters/bestiary/young_blue_dragon.json", "json/monsters/1.3/bestiary/young_blue_dragon.json", 9, "Bestiary", "legacy", nil},
		{"NPCs:200", "npcs", "Dragon Cultist", "1.3", "npcs/gamemastery_guide/dragon_cultist.json", "json/npcs/1.3/gamemastery_guide/dragon_cultist.json", 3, "Gamemastery Guide", "remastered", nil},
		{"Spells:300", "spells", "Dragon Breath", "1.3", "spells/core_rulebook/dragon_breath.json", "json/spells/1.3/core_rulebook/dragon_breath.json", 4, "Core Rulebook", "remastered", nil},
		{"Monsters:400", "monsters", "Orc Brute", "1.3", "monsters/bestiary/orc_brute.json", "json/monsters/1.3/bestiary/orc_brute.json", 0, "Bestiary", "remastered", nil},
		{"Monsters:401", "monsters", "Orc Warchief", "1.3", "monsters/bestiary/orc_warchief.json", "json/monsters/1.3/bestiary/orc_warchief.json", 2, "Bestiary", "remastered", nil},
		{"Monsters:402", "monsters", "Giant Porcupine", "1.3", "monsters/bestiary/giant_porcupine.json", "json/monsters/1.3/bestiary/giant_porcupine.json", 2, "Bestiary", "remastered", nil},
		{"Monsters:403", "monsters", "Orc Warrior", "1.3", "monsters/bestiary/orc_warrior.json", "json/monsters/1.3/bestiary/orc_warrior.json", 1, "Bestiary", "remastered", nil},
		{"Monsters:404", "monsters", "Orc Scrapper", "1.3", "monsters/monster_core/orc_scrapper.json", "json/monsters/1.3/monster_core/orc_scrapper.json", 1, "Monster Core", "remastered", nil},
		{"Monsters:405", "monsters", "Kobold Warrior", "1.3", "monsters/bestiary/kobold_warrior.json", "json/monsters/1.3/bestiary/kobold_warrior.json", -1, "Bestiary", "legacy", nil},
		{"Monsters:406", "monsters", "Kobold Warrior", "1.3", "monsters/monster_core/kobold_warrior.json", "json/monsters/1.3/monster_core/kobold_warrior.json", 0, "Monster Core", "remastered", nil},
		// Items — carry item_category/item_subcategory + traits (attrs set below).
		{"Equipment:900", "equipment", "Striking", "1.0", "equipment/rune/striking.json", "json/equipment/1.0/rune/striking.json", 4, "Player Core", "remastered", nil},
		{"Equipment:901", "equipment", "Frost", "1.0", "equipment/rune/frost.json", "json/equipment/1.0/rune/frost.json", 8, "Player Core", "remastered", nil},
		{"Equipment:902", "equipment", "Healing Potion", "1.0", "equipment/consumable/healing_potion.json", "json/equipment/1.0/consumable/healing_potion.json", 1, "Player Core", "remastered", nil},
		{"Armor:903", "armor", "Leather Armor", "1.0", "armor/base/leather_armor.json", "json/armor/1.0/base/leather_armor.json", 0, "Player Core", "remastered", nil},
	}

	for _, e := range entries {
		_, err := db.Exec(`
			INSERT INTO entries(game_id, aonid, type, name, current_schema_version,
				base_path, s3_key, level, source, edition, image_s3_key, attrs, search_text)
			VALUES(?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, '{}', ?)`,
			e.gameID, e.typ, e.name, e.schema, e.basePath, e.s3Key,
			e.level, e.source, e.edition, e.imagePath, e.name)
		if err != nil {
			t.Fatalf("insert entry %s: %v", e.gameID, err)
		}
	}

	// Add traits to some entries for Search trait filtering tests
	if _, err := db.Exec(`UPDATE entries SET attrs = '{"traits":["Dragon","Fire"]}' WHERE game_id = 'Monsters:100'`); err != nil {
		t.Fatalf("update attrs: %v", err)
	}
	if _, err := db.Exec(`UPDATE entries SET attrs = '{"traits":["Dragon","Electricity"]}' WHERE game_id = 'Monsters:102'`); err != nil {
		t.Fatalf("update attrs: %v", err)
	}
	if _, err := db.Exec(`UPDATE entries SET attrs = '{"traits":["Orc","Humanoid"]}' WHERE game_id = 'Monsters:400'`); err != nil {
		t.Fatalf("update attrs: %v", err)
	}
	// Item attrs — traits + item_category/item_subcategory for facet/category tests.
	itemAttrs := map[string]string{
		"Equipment:900": `{"traits":["Evocation","Magical"],"item_category":"Runes","item_subcategory":"Fundamental Weapon Runes"}`,
		"Equipment:901": `{"traits":["Cold","Evocation","Magical"],"item_category":"Runes","item_subcategory":"Property Runes"}`,
		"Equipment:902": `{"traits":["Healing","Magical"],"item_category":"Consumables","item_subcategory":"Potions"}`,
		"Armor:903":     `{"traits":["Comfort"],"item_category":"Armor","item_subcategory":"Base Armor"}`,
	}
	for gameID, attrs := range itemAttrs {
		if _, err := db.Exec(`UPDATE entries SET attrs = ? WHERE game_id = ?`, attrs, gameID); err != nil {
			t.Fatalf("update item attrs %s: %v", gameID, err)
		}
	}

	// entry_versions: all entries have 1.3, some also have 1.2
	versions := []struct {
		gameID, schema, s3Key string
	}{
		{"Monsters:100", "1.3", "json/monsters/1.3/bestiary/adult_red_dragon.json"},
		{"Monsters:100", "1.2", "json/monsters/1.2/bestiary/adult_red_dragon.json"},
		{"Monsters:101", "1.3", "json/monsters/1.3/bestiary/pseudodragon.json"},
		{"Monsters:101", "1.2", "json/monsters/1.2/bestiary/pseudodragon.json"},
		{"Monsters:102", "1.3", "json/monsters/1.3/bestiary/young_blue_dragon.json"},
		// 102 intentionally has NO 1.2 version
		{"NPCs:200", "1.3", "json/npcs/1.3/gamemastery_guide/dragon_cultist.json"},
		{"NPCs:200", "1.2", "json/npcs/1.2/gamemastery_guide/dragon_cultist.json"},
		{"Spells:300", "1.3", "json/spells/1.3/core_rulebook/dragon_breath.json"},
	}
	for _, v := range versions {
		_, err := db.Exec(`INSERT INTO entry_versions(game_id, schema_version, s3_key) VALUES(?, ?, ?)`,
			v.gameID, v.schema, v.s3Key)
		if err != nil {
			t.Fatalf("insert version %s/%s: %v", v.gameID, v.schema, err)
		}
	}

	// Alternates: Orc Brute (legacy) ↔ Orc Scrapper (remastered), Kobold Warrior ↔ Kobold Warrior
	alternates := []struct{ gameID, altGameID, altType string }{
		{"Monsters:400", "Monsters:404", "remastered"},
		{"Monsters:404", "Monsters:400", "legacy"},
		{"Monsters:405", "Monsters:406", "remastered"},
		{"Monsters:406", "Monsters:405", "legacy"},
	}
	for _, a := range alternates {
		_, err := db.Exec(`INSERT INTO alternates(game_id, alternate_game_id, alternate_type) VALUES(?, ?, ?)`,
			a.gameID, a.altGameID, a.altType)
		if err != nil {
			t.Fatalf("insert alternate %s→%s: %v", a.gameID, a.altGameID, err)
		}
	}

	// Sources table
	sources := []struct {
		name  string
		aonid *int
	}{
		{"Bestiary", intPtr(2)},
		{"Core Rulebook", intPtr(1)},
		{"Gamemastery Guide", intPtr(3)},
		{"Monster Core", nil},
	}
	for _, s := range sources {
		_, err := db.Exec(`INSERT INTO sources(name, aonid) VALUES(?, ?)`, s.name, s.aonid)
		if err != nil {
			t.Fatalf("insert source %s: %v", s.name, err)
		}
	}

	// Rebuild FTS indexes
	if _, err := db.Exec("INSERT INTO entries_fts(entries_fts) VALUES('rebuild')"); err != nil {
		t.Fatalf("rebuild fts: %v", err)
	}
	if _, err := db.Exec("INSERT INTO entries_trigram(entries_trigram) VALUES('rebuild')"); err != nil {
		t.Fatalf("rebuild trigram: %v", err)
	}
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

// ---------------------------------------------------------------------------
// Suggest tests
// ---------------------------------------------------------------------------

func TestSuggest_ShortQuery(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{Q: "dr"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results for short query, got %d", len(results))
	}
	if results == nil {
		t.Error("expected non-nil slice, got nil")
	}
}

func TestSuggest_SubstringMatch(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{Q: "dragon"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should match: Adult Red Dragon, Pseudodragon, Young Blue Dragon, Dragon Cultist, Dragon Breath
	if len(results) != 5 {
		t.Errorf("expected 5 results for 'dragon', got %d", len(results))
		for _, r := range results {
			t.Logf("  %s: %s", r.GameID, r.Name)
		}
	}
}

func TestSuggest_CarriesItemCategory(t *testing.T) {
	db := testDB(t)
	// The plain /search/suggest path shares the same SELECT/scan as suggestShortQuery,
	// so this guards both against an off-by-one in the added category columns.
	results, err := Suggest(context.Background(), db, SuggestParams{Q: "striking"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var s *Suggestion
	for i := range results {
		if results[i].Name == "Striking" {
			s = &results[i]
		}
	}
	if s == nil {
		t.Fatalf("Striking not in results: %+v", results)
	}
	if s.ItemCategory == nil || *s.ItemCategory != "Runes" ||
		s.ItemSubcategory == nil || *s.ItemSubcategory != "Fundamental Weapon Runes" {
		t.Errorf("category=%v subcategory=%v, want Runes / Fundamental Weapon Runes", s.ItemCategory, s.ItemSubcategory)
	}
}

func TestSuggest_TypeFilter(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{
		Q:     "dragon",
		Types: []string{"monsters"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only monsters: Adult Red Dragon, Pseudodragon, Young Blue Dragon
	if len(results) != 3 {
		t.Errorf("expected 3 monster results, got %d", len(results))
	}
	for _, r := range results {
		if r.Type != "monsters" {
			t.Errorf("expected type=monsters, got %s for %s", r.Type, r.Name)
		}
	}
}

func TestSuggest_MultiTypeFilter(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{
		Q:     "dragon",
		Types: []string{"monsters", "npcs"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Monsters (3) + NPCs (1) = 4
	if len(results) != 4 {
		t.Errorf("expected 4 results for monsters+npcs, got %d", len(results))
	}
}

func TestSuggest_VersionFilter(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{
		Q:       "dragon",
		Types:   []string{"monsters"},
		Version: "1.2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only monsters with 1.2: Adult Red Dragon, Pseudodragon (Young Blue has no 1.2)
	if len(results) != 2 {
		t.Errorf("expected 2 results with version=1.2, got %d", len(results))
		for _, r := range results {
			t.Logf("  %s: %s", r.GameID, r.Name)
		}
	}
}

func TestSuggest_LimitCap(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{
		Q:     "dragon",
		Limit: 2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results with limit=2, got %d", len(results))
	}
}

func TestSuggest_LimitDefault(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{
		Q:     "dragon",
		Limit: 0, // should default to 15
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// All 5 entries match, and default limit (15) is higher
	if len(results) != 5 {
		t.Errorf("expected 5 results with default limit, got %d", len(results))
	}
}

func TestSuggest_ImageIncluded(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{
		Q:     "pseudodragon",
		Types: []string{"monsters"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ImageS3Key == nil || *results[0].ImageS3Key != "images/Monsters/Pseudodragon.webp" {
		t.Errorf("expected image key, got %v", results[0].ImageS3Key)
	}
}

func TestSuggest_NoMatch(t *testing.T) {
	db := testDB(t)
	results, err := Suggest(context.Background(), db, SuggestParams{Q: "zzzznothing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
	if results == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
}

func TestSuggest_WordBoundary(t *testing.T) {
	db := testDB(t)
	// "orc" matches substring: Orc Brute, Orc Warchief, Giant Porcupine, Orc Warrior
	results, err := Suggest(context.Background(), db, SuggestParams{
		Q:     "orc",
		Types: []string{"monsters"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 5 {
		t.Errorf("expected 5 results for 'orc' substring, got %d", len(results))
		for _, r := range results {
			t.Logf("  %s", r.Name)
		}
	}

	// "orc " (trailing space) should filter to word-boundary only — no Porcupine
	results, err = Suggest(context.Background(), db, SuggestParams{
		Q:     "orc ",
		Types: []string{"monsters"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 4 {
		t.Errorf("expected 4 results for 'orc ' word-boundary, got %d", len(results))
		for _, r := range results {
			t.Logf("  %s", r.Name)
		}
	}
	for _, r := range results {
		if r.Name == "Giant Porcupine" {
			t.Error("Porcupine should not match word-boundary 'orc '")
		}
	}
}

func TestSuggest_MultiWordPartial(t *testing.T) {
	db := testDB(t)
	// "orc w" — "orc" is complete word, "w" is partial substring
	results, err := Suggest(context.Background(), db, SuggestParams{
		Q:     "orc w",
		Types: []string{"monsters"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should match: Orc Warchief, Orc Warrior (orc at word boundary + contains "w")
	if len(results) != 2 {
		t.Errorf("expected 2 results for 'orc w', got %d", len(results))
		for _, r := range results {
			t.Logf("  %s", r.Name)
		}
	}
}

func TestSuggest_PrefixMatchFirst(t *testing.T) {
	db := testDB(t)
	// "dragon" should return prefix matches (Dragon Cultist, Dragon Breath) before
	// substring matches (Adult Red Dragon, Pseudodragon, Young Blue Dragon)
	results, err := Suggest(context.Background(), db, SuggestParams{Q: "dragon"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}
	// First results should be prefix matches
	for _, r := range results[:2] {
		if r.Name != "Dragon Breath" && r.Name != "Dragon Cultist" {
			t.Errorf("expected prefix match in first 2 results, got %q", r.Name)
		}
	}
}

// ---------------------------------------------------------------------------
// GetByGameID tests
// ---------------------------------------------------------------------------

func TestGetByGameID_Found(t *testing.T) {
	db := testDB(t)
	entry, err := GetByGameID(context.Background(), db, "Monsters:100")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry == nil {
		t.Fatal("expected entry, got nil")
	}
	if entry.Name != "Adult Red Dragon" {
		t.Errorf("expected 'Adult Red Dragon', got %q", entry.Name)
	}
	if entry.GameID != "Monsters:100" {
		t.Errorf("expected game_id 'Monsters:100', got %q", entry.GameID)
	}
}

func TestGetByGameID_NotFound(t *testing.T) {
	db := testDB(t)
	entry, err := GetByGameID(context.Background(), db, "Monsters:99999")
	if err != nil {
		t.Fatalf("expected nil error for missing entry, got: %v", err)
	}
	if entry != nil {
		t.Errorf("expected nil entry, got %+v", entry)
	}
}

// ---------------------------------------------------------------------------
// GetAlternateGameID tests
// ---------------------------------------------------------------------------

func TestGetAlternateGameID_Found(t *testing.T) {
	db := testDB(t)
	// Orc Brute (400, legacy) → alternate remastered is Orc Scrapper (404)
	altID, err := GetAlternateGameID(context.Background(), db, "Monsters:400", "remastered")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if altID != "Monsters:404" {
		t.Errorf("expected Monsters:404, got %q", altID)
	}
}

func TestGetAlternateGameID_NotFound(t *testing.T) {
	db := testDB(t)
	// Adult Red Dragon has no alternates
	altID, err := GetAlternateGameID(context.Background(), db, "Monsters:100", "legacy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if altID != "" {
		t.Errorf("expected empty string, got %q", altID)
	}
}

func TestGetAlternateGameID_WrongEdition(t *testing.T) {
	db := testDB(t)
	// Orc Brute (400) has a remastered alternate but not a legacy one
	altID, err := GetAlternateGameID(context.Background(), db, "Monsters:400", "legacy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if altID != "" {
		t.Errorf("expected empty string for wrong edition, got %q", altID)
	}
}

func TestGetAlternateGameID_NonexistentEntry(t *testing.T) {
	db := testDB(t)
	altID, err := GetAlternateGameID(context.Background(), db, "Monsters:99999", "remastered")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if altID != "" {
		t.Errorf("expected empty string for nonexistent entry, got %q", altID)
	}
}

// ---------------------------------------------------------------------------
// SuggestUnified tests
// ---------------------------------------------------------------------------

func TestSuggestUnified_WithAlternate(t *testing.T) {
	db := testDB(t)
	results, err := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Q: "orc brute"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "Orc Brute" {
		t.Errorf("expected 'Orc Brute', got %q", results[0].Name)
	}
	if results[0].Alternate == nil {
		t.Fatal("expected alternate")
	}
	if results[0].Alternate.Name != "Orc Scrapper" {
		t.Errorf("expected alternate 'Orc Scrapper', got %q", results[0].Alternate.Name)
	}
}

func TestSuggestUnified_SameNameRemasteredWins(t *testing.T) {
	db := testDB(t)
	results, err := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Q: "kobold warrior"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	kobolds := 0
	for _, s := range results {
		if s.Name == "Kobold Warrior" {
			kobolds++
			if s.Edition == nil || *s.Edition != "remastered" {
				t.Errorf("expected remastered primary, got %v", s.Edition)
			}
			if s.Alternate == nil {
				t.Fatal("expected legacy alternate")
			}
			if s.Alternate.Edition == nil || *s.Alternate.Edition != "legacy" {
				t.Errorf("expected legacy alternate, got %v", s.Alternate.Edition)
			}
		}
	}
	if kobolds != 1 {
		t.Errorf("expected 1 Kobold Warrior, got %d", kobolds)
	}
}

func TestDeduplicateUnified_LegacyOnlyMatchSwapsToRemastered(t *testing.T) {
	legacy := "legacy"
	remastered := "remastered"

	// Only the legacy entry appeared in search results (matched the query).
	// The remastered alternate is attached via JOIN but didn't match the query itself.
	rawRows := []unifiedRawRow{
		{
			primary:   UnifiedSuggestion{GameID: "Monsters:OLD", Name: "Orc Brute", Type: "monsters", Edition: &legacy},
			alternate: &UnifiedSuggestion{GameID: "Monsters:NEW", Name: "Orc Scrapper", Type: "monsters", Edition: &remastered},
		},
	}

	results := deduplicateUnified(rawRows, 15)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Name != "Orc Scrapper" {
		t.Errorf("expected remastered 'Orc Scrapper' as primary, got %q", r.Name)
	}
	if r.Edition == nil || *r.Edition != "remastered" {
		t.Errorf("expected remastered edition, got %v", r.Edition)
	}
	if r.Alternate == nil {
		t.Fatal("expected legacy alternate")
	}
	if r.Alternate.Name != "Orc Brute" {
		t.Errorf("expected legacy 'Orc Brute' as alternate, got %q", r.Alternate.Name)
	}
}

func TestDeduplicateUnified_RemasteredPrimaryUnchanged(t *testing.T) {
	legacy := "legacy"
	remastered := "remastered"

	// Remastered is already primary — should stay unchanged.
	rawRows := []unifiedRawRow{
		{
			primary:   UnifiedSuggestion{GameID: "Monsters:NEW", Name: "Wolf", Type: "monsters", Edition: &remastered},
			alternate: &UnifiedSuggestion{GameID: "Monsters:OLD", Name: "Wolf", Type: "monsters", Edition: &legacy},
		},
	}

	results := deduplicateUnified(rawRows, 15)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "Wolf" || *results[0].Edition != "remastered" {
		t.Error("remastered primary should be unchanged")
	}
	if results[0].Alternate == nil || *results[0].Alternate.Edition != "legacy" {
		t.Error("legacy alternate should be preserved")
	}
}

func TestSuggestUnified_ShortQuery(t *testing.T) {
	db := testDB(t)
	// 2-char query should work via LIKE
	results, err := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Q: "or"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results for 2-char query 'or'")
	}
}

func TestSuggestUnified_TypeFilter(t *testing.T) {
	db := testDB(t)
	results, err := SuggestUnified(context.Background(), db, UnifiedSuggestParams{
		Q:     "dragon",
		Types: []string{"spells"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range results {
		if s.Type != "spells" {
			t.Errorf("expected type=spells, got %q for %q", s.Type, s.Name)
		}
	}
}

func TestSuggestUnified_CarriesItemCategory(t *testing.T) {
	db := testDB(t)
	// An item row carries item_category/item_subcategory (as first-class as traits).
	results, err := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Q: "striking"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var striking *UnifiedSuggestion
	for i := range results {
		if results[i].Name == "Striking" {
			striking = &results[i]
		}
	}
	if striking == nil {
		t.Fatalf("Striking not in results: %+v", results)
	}
	if striking.ItemCategory == nil || *striking.ItemCategory != "Runes" {
		t.Errorf("item_category = %v, want Runes", striking.ItemCategory)
	}
	if striking.ItemSubcategory == nil || *striking.ItemSubcategory != "Fundamental Weapon Runes" {
		t.Errorf("item_subcategory = %v, want Fundamental Weapon Runes", striking.ItemSubcategory)
	}

	// A creature has no item_category → nil (omitted from JSON).
	cres, err := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Q: "dragon"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range cres {
		if s.Type != "equipment" && s.Type != "armor" && s.ItemCategory != nil {
			t.Errorf("%s (%s) should have nil item_category, got %v", s.Name, s.Type, *s.ItemCategory)
		}
	}
}

func TestSuggestUnified_VersionFilter(t *testing.T) {
	db := testDB(t)
	// Young Blue Dragon (Monsters:102) has no 1.2 version
	results, err := SuggestUnified(context.Background(), db, UnifiedSuggestParams{
		Q:       "dragon",
		Version: "1.2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range results {
		if s.GameID == "Monsters:102" {
			t.Error("Young Blue Dragon should be excluded (no 1.2 version)")
		}
	}
}

func TestSuggestUnified_LimitClamping(t *testing.T) {
	db := testDB(t)
	results, err := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Q: "orc", Limit: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) > 15 {
		t.Errorf("limit=0 should clamp to 15, got %d results", len(results))
	}

	results, err = SuggestUnified(context.Background(), db, UnifiedSuggestParams{Q: "orc", Limit: 100})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) > 15 {
		t.Errorf("limit=100 should clamp to 15, got %d results", len(results))
	}
}

func TestSuggestUnified_EmptyQuery(t *testing.T) {
	db := testDB(t)
	results, err := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Q: ""})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// Search tests
// ---------------------------------------------------------------------------

func TestSearch_NoFilters(t *testing.T) {
	db := testDB(t)
	result, err := Search(context.Background(), db, SearchParams{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 16 {
		t.Errorf("expected 16 total entries, got %d", result.Total)
	}
}

func TestSearch_FTSQuery(t *testing.T) {
	db := testDB(t)
	result, err := Search(context.Background(), db, SearchParams{Q: "dragon"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total == 0 {
		t.Fatal("expected results for 'dragon'")
	}
	found := false
	for _, e := range result.Results {
		if e.Type == "spells" && e.Name == "Dragon Breath" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find 'Dragon Breath' spell in search results for 'dragon'")
	}
}

func TestSearch_TypeFilter(t *testing.T) {
	db := testDB(t)
	result, err := Search(context.Background(), db, SearchParams{Type: "monsters"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range result.Results {
		if e.Type != "monsters" {
			t.Errorf("expected type=monsters, got %q for %q", e.Type, e.Name)
		}
	}
}

func TestSearch_SourceFilter(t *testing.T) {
	db := testDB(t)
	result, err := Search(context.Background(), db, SearchParams{Source: "Bestiary"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range result.Results {
		if *e.Source != "Bestiary" {
			t.Errorf("expected source=Bestiary, got %q", *e.Source)
		}
	}
}

func TestSearch_EditionFilter(t *testing.T) {
	db := testDB(t)
	result, err := Search(context.Background(), db, SearchParams{Edition: "legacy"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total == 0 {
		t.Error("expected legacy results")
	}
	for _, e := range result.Results {
		if *e.Edition != "legacy" {
			t.Errorf("expected edition=legacy, got %q", *e.Edition)
		}
	}
}

func TestSearch_LevelExact(t *testing.T) {
	db := testDB(t)
	result, err := Search(context.Background(), db, SearchParams{Level: "14"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 1 {
		t.Fatalf("expected 1 result for level=14, got %d", result.Total)
	}
	if result.Results[0].Name != "Adult Red Dragon" {
		t.Errorf("expected Adult Red Dragon, got %q", result.Results[0].Name)
	}
}

func TestSearch_LevelRange(t *testing.T) {
	db := testDB(t)
	result, err := Search(context.Background(), db, SearchParams{Level: "0-2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range result.Results {
		if e.Level == nil || *e.Level < 0 || *e.Level > 2 {
			t.Errorf("entry %q level %v outside range 0-2", e.Name, e.Level)
		}
	}
}

func TestSearch_Pagination(t *testing.T) {
	db := testDB(t)
	page1, err := Search(context.Background(), db, SearchParams{Limit: 3, Offset: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	page2, err := Search(context.Background(), db, SearchParams{Limit: 3, Offset: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(page1.Results) != 3 {
		t.Errorf("expected 3 results on page 1, got %d", len(page1.Results))
	}
	if len(page2.Results) != 3 {
		t.Errorf("expected 3 results on page 2, got %d", len(page2.Results))
	}
	// Pages should not overlap
	page1IDs := map[string]bool{}
	for _, r := range page1.Results {
		page1IDs[r.GameID] = true
	}
	for _, r := range page2.Results {
		if page1IDs[r.GameID] {
			t.Errorf("page 2 contains overlapping game ID: %s", r.GameID)
		}
	}
}

func TestSearch_DefaultLimit(t *testing.T) {
	db := testDB(t)
	result, err := Search(context.Background(), db, SearchParams{Limit: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Default limit is 20, we have 16 entries
	if len(result.Results) != 16 {
		t.Errorf("expected all 16 entries with default limit, got %d", len(result.Results))
	}
}

// ---------------------------------------------------------------------------
// List tests
// ---------------------------------------------------------------------------

func TestList_ByType(t *testing.T) {
	db := testDB(t)
	result, err := List(context.Background(), db, ListParams{Type: "npcs", Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("expected 1 npc, got %d", result.Total)
	}
	if result.Results[0].Name != "Dragon Cultist" {
		t.Errorf("expected Dragon Cultist, got %q", result.Results[0].Name)
	}
}

func TestList_WithEditionFilter(t *testing.T) {
	db := testDB(t)
	result, err := List(context.Background(), db, ListParams{Type: "monsters", Edition: "legacy", Limit: 20})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range result.Results {
		if *e.Edition != "legacy" {
			t.Errorf("expected legacy edition, got %q for %q", *e.Edition, e.Name)
		}
	}
}

// ---------------------------------------------------------------------------
// GetVersions tests
// ---------------------------------------------------------------------------

func TestGetVersions_Found(t *testing.T) {
	db := testDB(t)
	versions, err := GetVersions(context.Background(), db, "Monsters:100")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions for Monsters:100, got %d", len(versions))
	}
	// Ordered by schema_version
	if versions[0].SchemaVersion != "1.2" {
		t.Errorf("expected first version 1.2, got %q", versions[0].SchemaVersion)
	}
	if versions[1].SchemaVersion != "1.3" {
		t.Errorf("expected second version 1.3, got %q", versions[1].SchemaVersion)
	}
}

func TestGetVersions_NotFound(t *testing.T) {
	db := testDB(t)
	versions, err := GetVersions(context.Background(), db, "Monsters:99999")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("expected 0 versions, got %d", len(versions))
	}
}

func TestGetVersions_SingleVersion(t *testing.T) {
	db := testDB(t)
	// Young Blue Dragon only has 1.3
	versions, err := GetVersions(context.Background(), db, "Monsters:102")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(versions))
	}
	if versions[0].SchemaVersion != "1.3" {
		t.Errorf("expected 1.3, got %q", versions[0].SchemaVersion)
	}
}

// ---------------------------------------------------------------------------
// Types tests
// ---------------------------------------------------------------------------

func TestTypes(t *testing.T) {
	db := testDB(t)
	types, err := Types(context.Background(), db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(types) != 5 {
		t.Fatalf("expected 5 types (armor, equipment, monsters, npcs, spells), got %d", len(types))
	}
	typeMap := map[string]int{}
	for _, tc := range types {
		typeMap[tc.Type] = tc.Count
	}
	if typeMap["monsters"] != 10 {
		t.Errorf("expected 10 monsters, got %d", typeMap["monsters"])
	}
	if typeMap["npcs"] != 1 {
		t.Errorf("expected 1 npc, got %d", typeMap["npcs"])
	}
	if typeMap["spells"] != 1 {
		t.Errorf("expected 1 spell, got %d", typeMap["spells"])
	}
}

// ---------------------------------------------------------------------------
// Sources tests
// ---------------------------------------------------------------------------

func TestSources(t *testing.T) {
	db := testDB(t)
	sources, err := Sources(context.Background(), db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sources) != 4 {
		t.Fatalf("expected 4 sources, got %d", len(sources))
	}
	// Verify ordering (alphabetical)
	names := make([]string, len(sources))
	for i, s := range sources {
		name, ok := s["name"].(string)
		if !ok {
			t.Fatalf("source at index %d has missing or invalid name", i)
		}
		names[i] = name
	}
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("sources not sorted: %v", names)
			break
		}
	}
}

// ---------------------------------------------------------------------------
// Search traits filter tests
// ---------------------------------------------------------------------------

func TestSearch_SingleTrait(t *testing.T) {
	db := testDB(t)
	result, err := Search(context.Background(), db, SearchParams{Traits: "Dragon"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 2 {
		t.Errorf("expected 2 results with Dragon trait, got %d", result.Total)
	}
	for _, e := range result.Results {
		if e.Name != "Adult Red Dragon" && e.Name != "Young Blue Dragon" {
			t.Errorf("unexpected entry %q in Dragon trait results", e.Name)
		}
	}
}

func TestSearch_MultipleTraits(t *testing.T) {
	db := testDB(t)
	// Both traits must match (AND logic)
	result, err := Search(context.Background(), db, SearchParams{Traits: "Dragon,Fire"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 1 {
		t.Fatalf("expected 1 result with Dragon+Fire traits, got %d", result.Total)
	}
	if result.Results[0].Name != "Adult Red Dragon" {
		t.Errorf("expected Adult Red Dragon, got %q", result.Results[0].Name)
	}
}

func TestSearch_TraitNoMatch(t *testing.T) {
	db := testDB(t)
	result, err := Search(context.Background(), db, SearchParams{Traits: "Undead"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("expected 0 results for Undead trait, got %d", result.Total)
	}
}

// ---------------------------------------------------------------------------
// Facets + trait-suggest + attr-filter tests
// ---------------------------------------------------------------------------

func TestFacets_CategoriesAndSubcategories(t *testing.T) {
	db := testDB(t)
	facets, err := Facets(context.Background(), db, []string{"equipment", "armor"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string][]string{
		"Runes":       {"Fundamental Weapon Runes", "Property Runes"}, // sorted
		"Consumables": {"Potions"},
		"Armor":       {"Base Armor"},
	}
	if len(facets) != len(want) {
		t.Fatalf("expected %d categories, got %d (%v)", len(want), len(facets), facets)
	}
	for cat, subs := range want {
		if !slices.Equal(facets[cat], subs) {
			t.Errorf("category %q: want %v, got %v", cat, subs, facets[cat])
		}
	}
}

func TestFacets_TypeFilter(t *testing.T) {
	db := testDB(t)
	facets, err := Facets(context.Background(), db, []string{"armor"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(facets) != 1 || !slices.Equal(facets["Armor"], []string{"Base Armor"}) {
		t.Errorf("expected only Armor->[Base Armor], got %v", facets)
	}
}

func TestFacets_ExcludesCreatures(t *testing.T) {
	db := testDB(t)
	facets, err := Facets(context.Background(), db, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Creatures carry no item_category, so only the 3 item categories appear.
	if len(facets) != 3 {
		t.Errorf("expected 3 item categories, got %d (%v)", len(facets), facets)
	}
}

func TestSuggestTraits_Base(t *testing.T) {
	db := testDB(t)
	traits, err := SuggestTraits(context.Background(), db, TraitSuggestParams{Types: []string{"equipment"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Distinct traits across the 3 equipment items (armor "Comfort" excluded), sorted.
	if !slices.Equal(traits, []string{"Cold", "Evocation", "Healing", "Magical"}) {
		t.Errorf("unexpected traits: %v", traits)
	}
}

func TestSuggestTraits_CoOccurrence(t *testing.T) {
	db := testDB(t)
	// Only Frost carries Cold → co-occurring traits are Evocation, Magical.
	traits, err := SuggestTraits(context.Background(), db, TraitSuggestParams{Types: []string{"equipment"}, Selected: []string{"Cold"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(traits, []string{"Evocation", "Magical"}) {
		t.Errorf("want [Evocation Magical], got %v", traits)
	}
}

func TestSuggestTraits_ExcludesSelectedCaseInsensitive(t *testing.T) {
	db := testDB(t)
	// Lowercase "magical" must match + be excluded from the output.
	traits, err := SuggestTraits(context.Background(), db, TraitSuggestParams{Types: []string{"equipment"}, Selected: []string{"magical"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, tr := range traits {
		if strings.EqualFold(tr, "magical") {
			t.Fatalf("selected trait must be excluded, got %v", traits)
		}
	}
	if !slices.Equal(traits, []string{"Cold", "Evocation", "Healing"}) {
		t.Errorf("unexpected traits: %v", traits)
	}
}

func TestSuggestTraits_Prefix(t *testing.T) {
	db := testDB(t)
	traits, err := SuggestTraits(context.Background(), db, TraitSuggestParams{Types: []string{"equipment"}, Q: "ev"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(traits, []string{"Evocation"}) {
		t.Errorf("want [Evocation], got %v", traits)
	}
}

func TestSuggest_CategoryFilter(t *testing.T) {
	db := testDB(t)
	got, err := Suggest(context.Background(), db, SuggestParams{Q: "frost", Category: "Runes"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Frost" {
		t.Errorf("want [Frost], got %v", got)
	}
	none, err := Suggest(context.Background(), db, SuggestParams{Q: "frost", Category: "Armor"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("want no results for Frost filtered to Armor, got %v", none)
	}
}

func TestSuggest_TraitFilterCaseInsensitive(t *testing.T) {
	db := testDB(t)
	got, err := Suggest(context.Background(), db, SuggestParams{Q: "frost", Traits: "cold"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Frost" {
		t.Errorf("want [Frost] for trait 'cold', got %v", got)
	}
}

func TestSearch_CategorySubcategoryFilter(t *testing.T) {
	db := testDB(t)
	res, err := Search(context.Background(), db, SearchParams{Category: "Runes", Subcategory: "Property Runes"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Total != 1 || len(res.Results) != 1 || res.Results[0].Name != "Frost" {
		t.Errorf("want [Frost], got total=%d %v", res.Total, res.Results)
	}
}

func TestSuggestTraits_LimitCap(t *testing.T) {
	db := testDB(t)
	// Seed one entry carrying 51 distinct traits (isolated via a unique type) so
	// the >50 clamp is load-bearing: without it this returns 51 and the test fails.
	names := make([]string, 51)
	for i := range names {
		names[i] = fmt.Sprintf("t%02d", i)
	}
	attrs, err := json.Marshal(map[string][]string{"traits": names})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO entries(game_id, type, name, current_schema_version, base_path, s3_key, source, edition, attrs, search_text)
		VALUES('Gadget:1','gadget','Widget','1.0','g/w.json','json/g/1.0/w.json','Player Core','remastered',?,'Widget')`, string(attrs)); err != nil {
		t.Fatalf("insert: %v", err)
	}
	traits, err := SuggestTraits(context.Background(), db, TraitSuggestParams{Types: []string{"gadget"}, Limit: 100})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(traits) != 50 {
		t.Errorf("51 distinct traits with Limit:100 must clamp to 50, got %d", len(traits))
	}
}

func TestFacets_CategoryWithoutSubcategory(t *testing.T) {
	db := testDB(t)
	// An item with item_category set but no item_subcategory must still list,
	// with an empty subcategory slice.
	if _, err := db.Exec(`INSERT INTO entries(game_id, type, name, current_schema_version, base_path, s3_key, source, edition, attrs, search_text)
		VALUES('Equipment:910','equipment','Bag of Holding','1.0','equipment/gear/bag.json','json/equipment/1.0/gear/bag.json','Player Core','remastered','{"item_category":"Adventuring Gear"}','Bag of Holding')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	facets, err := Facets(context.Background(), db, []string{"equipment"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	subs, ok := facets["Adventuring Gear"]
	if !ok {
		t.Fatalf("expected 'Adventuring Gear' category to list, got %v", facets)
	}
	if len(subs) != 0 {
		t.Errorf("expected empty subcategory slice, got %v", subs)
	}
}

func TestSuggest_ShortQueryWithFilter(t *testing.T) {
	db := testDB(t)
	// The <3-char path uses exact name match (no trigram) — insert a short-named
	// item so we can drive it with a filter. Exact match needs no FTS rebuild.
	if _, err := db.Exec(`INSERT INTO entries(game_id, type, name, current_schema_version, base_path, s3_key, source, edition, attrs, search_text)
		VALUES('Equipment:920','equipment','Xy','1.0','equipment/rune/xy.json','json/equipment/1.0/rune/xy.json','Player Core','remastered','{"traits":["Magical"],"item_category":"Runes","item_subcategory":"Property Runes"}','Xy')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := Suggest(context.Background(), db, SuggestParams{Q: "Xy", Category: "Runes"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Xy" {
		t.Errorf("want [Xy] for short query + Category=Runes, got %v", got)
	}
	none, err := Suggest(context.Background(), db, SuggestParams{Q: "Xy", Category: "Armor"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("want no results for Xy filtered to Armor, got %v", none)
	}
}

func TestSuggestUnified_AttrFilter(t *testing.T) {
	db := testDB(t)
	got, err := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Q: "frost", Category: "Runes"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Frost" {
		t.Errorf("want [Frost], got %v", got)
	}
	none, err := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Q: "frost", Category: "Armor"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("want no results for Frost filtered to Armor, got %v", none)
	}
}

func TestSuggestTraits_FacetNarrows(t *testing.T) {
	db := testDB(t)
	// Category narrows co-occurrence: Consumables → only Healing Potion's traits.
	byCat, err := SuggestTraits(context.Background(), db, TraitSuggestParams{Types: []string{"equipment"}, Category: "Consumables"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !slices.Equal(byCat, []string{"Healing", "Magical"}) {
		t.Errorf("want [Healing Magical] under Consumables, got %v", byCat)
	}
	// Subcategory narrows further: Fundamental Weapon Runes → only Striking, so
	// from the full equipment base [Cold Evocation Healing Magical], Cold (Frost,
	// a Property Rune) and Healing (a Consumable) both drop.
	bySub, err := SuggestTraits(context.Background(), db, TraitSuggestParams{Types: []string{"equipment"}, Subcategory: "Fundamental Weapon Runes"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !slices.Equal(bySub, []string{"Evocation", "Magical"}) {
		t.Errorf("want [Evocation Magical] under Fundamental Weapon Runes, got %v", bySub)
	}
}

func TestSuggestUnified_FilterOnly(t *testing.T) {
	db := testDB(t)
	// Empty query + a category filter lists the matching entries in name order,
	// so a picker populates without typing.
	byCat, err := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Types: []string{"equipment"}, Category: "Runes"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	names := make([]string, len(byCat))
	for i, s := range byCat {
		names[i] = s.Name
	}
	if !slices.Equal(names, []string{"Frost", "Striking"}) {
		t.Errorf("want [Frost Striking] for empty q + Category=Runes, got %v", names)
	}
	// Empty query + a trait filter narrows to the co-matching entry.
	byTrait, err := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Types: []string{"equipment"}, Traits: "Cold"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(byTrait) != 1 || byTrait[0].Name != "Frost" {
		t.Errorf("want [Frost] for empty q + Traits=Cold, got %v", byTrait)
	}
	// Empty query + a subcategory filter narrows to that subcategory (Frost is a
	// Property Rune; Striking is a Fundamental Weapon Rune).
	bySub, err := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Types: []string{"equipment"}, Category: "Runes", Subcategory: "Property Runes"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(bySub) != 1 || bySub[0].Name != "Frost" {
		t.Errorf("want [Frost] for empty q + Property Runes, got %v", bySub)
	}
	// A degenerate filter that yields no real condition (traits=",") must not dump
	// the catalog — it's treated as no filter.
	degenerate, err := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Types: []string{"equipment"}, Traits: ","})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(degenerate) != 0 {
		t.Errorf("want [] for a degenerate empty-token filter, got %v", degenerate)
	}
	// Empty query + NO filter → nothing (don't dump the catalog on type alone).
	none, err := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Types: []string{"equipment"}})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("want no results for empty q + no filter, got %v", none)
	}
}

func TestSuggestUnified_LevelFilter(t *testing.T) {
	db := testDB(t)
	names := func(rs []UnifiedSuggestion) []string {
		out := make([]string, len(rs))
		for i, r := range rs {
			out[i] = r.Name
		}
		return out
	}
	// Dragons (type=monsters, no alternates): Adult Red Dragon(14), Pseudodragon(2),
	// Young Blue Dragon(9).
	min9, err := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Q: "dragon", Types: []string{"monsters"}, LevelMin: "9"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !slices.Equal(names(min9), []string{"Adult Red Dragon", "Young Blue Dragon"}) {
		t.Errorf("level>=9: want [Adult Red Dragon, Young Blue Dragon], got %v", names(min9))
	}
	max9, _ := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Q: "dragon", Types: []string{"monsters"}, LevelMax: "9"})
	if !slices.Equal(names(max9), []string{"Pseudodragon", "Young Blue Dragon"}) {
		t.Errorf("level<=9: want [Pseudodragon, Young Blue Dragon], got %v", names(max9))
	}
	exact, _ := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Q: "dragon", Types: []string{"monsters"}, LevelMin: "9", LevelMax: "9"})
	if !slices.Equal(names(exact), []string{"Young Blue Dragon"}) {
		t.Errorf("level 9-9: want [Young Blue Dragon], got %v", names(exact))
	}
	// A negative bound parses and applies: no dragon is level <= -2.
	none, _ := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Q: "dragon", Types: []string{"monsters"}, LevelMax: "-2"})
	if len(none) != 0 {
		t.Errorf("level<=-2: want [], got %v", names(none))
	}
	// Filter-only browse: a level bound alone (no query) lists matches.
	browse, _ := SuggestUnified(context.Background(), db, UnifiedSuggestParams{Types: []string{"monsters"}, LevelMin: "14"})
	if !slices.Equal(names(browse), []string{"Adult Red Dragon"}) {
		t.Errorf("filter-only level>=14: want [Adult Red Dragon], got %v", names(browse))
	}
}

func TestEquipmentByAttr_HostAndEditionScoped(t *testing.T) {
	d := testDB(t)
	ins := func(gameID, edition, attrs string) {
		if _, err := d.Exec(`INSERT INTO entries(game_id, type, name, current_schema_version,
			base_path, s3_key, edition, attrs) VALUES(?,?,?,?,?,?,?,?)`,
			gameID, "equipment", gameID, "1.3", "equipment/b/x.json", "json/equipment/1.3/b/x.json", edition, attrs); err != nil {
			t.Fatalf("insert %s: %v", gameID, err)
		}
	}
	ins("re", "remastered", `{"rune_host":"weapon"}`)
	ins("leg", "legacy", `{"rune_host":"weapon"}`)
	ins("armor", "remastered", `{"rune_host":"armor"}`)
	// A rune with no cross-edition counterpart (edition-agnostic).
	if _, err := d.Exec(`INSERT INTO entries(game_id, type, name, current_schema_version,
		base_path, s3_key, edition, attrs) VALUES(?,?,?,?,?,?,NULL,?)`,
		"agnostic", "equipment", "agnostic", "1.3", "equipment/b/x.json", "json/equipment/1.3/b/x.json",
		`{"rune_host":"weapon"}`); err != nil {
		t.Fatalf("insert agnostic: %v", err)
	}

	got, err := EquipmentByAttr(context.Background(), d, "$.rune_host", "weapon", "remastered")
	if err != nil {
		t.Fatal(err)
	}
	// Host filter drops the armor rune; edition filter drops the legacy one; the
	// edition-agnostic (NULL) rune is INCLUDED.
	ids := map[string]bool{}
	for _, e := range got {
		ids[e.GameID] = true
	}
	if len(got) != 2 || !ids["re"] || !ids["agnostic"] {
		t.Fatalf("got %v, want {re, agnostic} (NULL-edition included, legacy/armor excluded)", ids)
	}

	// Empty edition = no edition filter → all three weapon-host runes.
	all, err := EquipmentByAttr(context.Background(), d, "$.rune_host", "weapon", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("unfiltered got %d, want 3", len(all))
	}
}
