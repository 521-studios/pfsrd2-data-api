// Package api implements the HTTP handlers for the pfsrd2 data API.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/521studios/pfsrd2-data-api/internal/db"
	"github.com/521studios/pfsrd2-data-api/internal/defects"
	"github.com/521studios/pfsrd2-data-api/internal/s3"
	"github.com/521studios/pfsrd2-data-api/internal/startup"
	"github.com/521studios/pfsrd2-data-api/internal/template"
)

// Config holds handler dependencies injected at startup.
type Config struct {
	S3Client    s3.ObjectFetcher
	ImageDomain string // app domain for image redirects, e.g. "lets-roll.org"
	StartupCfg  startup.Config
}

// ---------------------------------------------------------------------------
// Router
// ---------------------------------------------------------------------------

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.RequestURI(),
			"status", sw.status,
			"bytes", sw.bytes,
			"duration", time.Since(start).Round(time.Millisecond),
		)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func NewRouter(cfg Config) *chi.Mux {
	r := chi.NewRouter()
	r.Use(requestLogger)
	h := &handler{cfg: cfg}

	r.Route("/api/pfsrd2", func(r chi.Router) {
		r.Get("/search", h.search)
		r.Get("/search/suggest", h.suggest)
		r.Get("/search/suggest/unified", h.suggestUnified)
		r.Get("/types", h.types)
		r.Get("/sources", h.sources)
		r.Get("/entries/{gameID}", h.getEntry)
		r.Get("/entries/{gameID}/full", h.getEntryFull)
		r.Get("/images/{category}/{filename}", h.serveImage)
		r.Get("/db/status", h.dbStatus)
		r.Post("/db/refresh", h.dbRefresh)

		// Template application
		r.Get("/templates/{monsterGameID}/apply/{templateGameID}", h.applyTemplateByID)
		r.Post("/templates/apply", h.applyTemplateInline)

		// /{type} — paginated list
		// /{type}/{schema_version}/{book}/{filename} — full JSON from S3
		r.Post("/defects", h.reportDefect)
		r.Get("/{type}", h.listType)
		r.Get("/{type}/{schemaVersion}/{book}/{filename}", h.getFullJSON)
	})

	return r
}

type handler struct {
	cfg Config
}

// ---------------------------------------------------------------------------
// GET /search
// ---------------------------------------------------------------------------

func (h *handler) search(w http.ResponseWriter, r *http.Request) {
	p := db.SearchParams{
		Q:            r.URL.Query().Get("q"),
		Type:         r.URL.Query().Get("type"),
		Level:        r.URL.Query().Get("level"),
		Source:       r.URL.Query().Get("source"),
		Edition:      r.URL.Query().Get("edition"),
		Traits:       r.URL.Query().Get("traits"),
		Limit:        queryInt(r, "limit", 20),
		Offset:       queryInt(r, "offset", 0),
		ApplicableTo: r.URL.Query().Get("applicable_to"),
	}
	result, err := db.Search(r.Context(), db.Global(), p)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, result)
}

// ---------------------------------------------------------------------------
// GET /search/suggest
// ---------------------------------------------------------------------------

func (h *handler) suggest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) == 0 {
		jsonOK(w, []db.Suggestion{})
		return
	}
	p := db.SuggestParams{
		Q:       q,
		Types:   r.URL.Query()["type"],
		Version: r.URL.Query().Get("version"),
		Limit:   queryInt(r, "limit", 15),
	}
	results, err := db.Suggest(r.Context(), db.Global(), p)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, results)
}

// ---------------------------------------------------------------------------
// GET /search/suggest/unified
// ---------------------------------------------------------------------------

func (h *handler) suggestUnified(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(q) == 0 {
		jsonOK(w, []db.UnifiedSuggestion{})
		return
	}
	p := db.UnifiedSuggestParams{
		Q:       q,
		Types:   r.URL.Query()["type"],
		Version: r.URL.Query().Get("version"),
		Limit:   queryInt(r, "limit", 15),
	}
	results, err := db.SuggestUnified(r.Context(), db.Global(), p)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, results)
}

// ---------------------------------------------------------------------------
// GET /types
// ---------------------------------------------------------------------------

func (h *handler) types(w http.ResponseWriter, r *http.Request) {
	types, err := db.Types(r.Context(), db.Global())
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, types)
}

// ---------------------------------------------------------------------------
// GET /sources
// ---------------------------------------------------------------------------

func (h *handler) sources(w http.ResponseWriter, r *http.Request) {
	sources, err := db.Sources(r.Context(), db.Global())
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, sources)
}

// ---------------------------------------------------------------------------
// GET /{type}
// ---------------------------------------------------------------------------

func (h *handler) listType(w http.ResponseWriter, r *http.Request) {
	p := db.ListParams{
		Type:         chi.URLParam(r, "type"),
		Source:       r.URL.Query().Get("source"),
		Edition:      r.URL.Query().Get("edition"),
		Level:        r.URL.Query().Get("level"),
		Limit:        queryInt(r, "limit", 20),
		Offset:       queryInt(r, "offset", 0),
		ApplicableTo: r.URL.Query().Get("applicable_to"),
	}
	result, err := db.List(r.Context(), db.Global(), p)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, result)
}

// ---------------------------------------------------------------------------
// GET /{type}/{schema_version}/{book}/{filename}
// ---------------------------------------------------------------------------

func (h *handler) getFullJSON(w http.ResponseWriter, r *http.Request) {
	typeName := chi.URLParam(r, "type")
	schemaVersion := chi.URLParam(r, "schemaVersion")
	book := chi.URLParam(r, "book")
	filename := chi.URLParam(r, "filename")
	key := strings.Join([]string{"json", typeName, schemaVersion, book, filename}, "/")

	body, err := h.cfg.S3Client.GetObjectBytes(r.Context(), key)
	if err != nil {
		if isNotFound(err) {
			jsonError(w, "not found", http.StatusNotFound)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(body); err != nil {
		slog.ErrorContext(r.Context(), "failed to write response", "key", key, "err", err)
	}
}

// ---------------------------------------------------------------------------
// GET /entries/{gameID}
// ---------------------------------------------------------------------------

func (h *handler) getEntry(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameID")
	entry, err := db.GetByGameID(r.Context(), db.Global(), gameID)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if entry == nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}
	// Include available versions (log but don't fail if lookup errors)
	versions, err := db.GetVersions(r.Context(), db.Global(), gameID)
	if err != nil {
		slog.WarnContext(r.Context(), "failed to fetch entry versions", "game_id", gameID, "err", err)
	}
	jsonOK(w, map[string]any{
		"entry":    entry,
		"versions": versions,
	})
}

// ---------------------------------------------------------------------------
// GET /entries/{gameID}/full
// ---------------------------------------------------------------------------

func (h *handler) getEntryFull(w http.ResponseWriter, r *http.Request) {
	gameID := chi.URLParam(r, "gameID")
	entry, err := db.GetByGameID(r.Context(), db.Global(), gameID)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if entry == nil {
		jsonError(w, "not found", http.StatusNotFound)
		return
	}

	// Honour ?version= to fetch a specific schema version
	wantVersion := r.URL.Query().Get("version")
	s3Key := entry.S3Key
	if wantVersion != "" {
		versions, verErr := db.GetVersions(r.Context(), db.Global(), gameID)
		if verErr != nil {
			slog.WarnContext(r.Context(), "failed to fetch entry versions", "game_id", gameID, "err", verErr)
		}
		for _, v := range versions {
			if v.SchemaVersion == wantVersion {
				s3Key = v.S3Key
				break
			}
		}
	}

	body, err := h.cfg.S3Client.GetObjectBytes(r.Context(), s3Key)
	if err != nil {
		if isNotFound(err) {
			jsonError(w, "not found in S3", http.StatusNotFound)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(body); err != nil {
		slog.ErrorContext(r.Context(), "failed to write response", "s3_key", s3Key, "err", err)
	}
}

// ---------------------------------------------------------------------------
// GET /images/{category}/{filename}
// ---------------------------------------------------------------------------

func (h *handler) serveImage(w http.ResponseWriter, r *http.Request) {
	category := chi.URLParam(r, "category")
	filename := chi.URLParam(r, "filename")

	if strings.Contains(category, "..") || strings.Contains(filename, "..") ||
		strings.Contains(category, "/") || strings.Contains(filename, "/") {
		http.Error(w, "invalid path component", http.StatusBadRequest)
		return
	}

	key := "images/" + category + "/" + filename

	body, err := h.cfg.S3Client.GetObject(r.Context(), key)
	if err != nil {
		if isNotFound(err) {
			http.NotFound(w, r)
		} else {
			slog.ErrorContext(r.Context(), "failed to get image from s3", "key", key, "err", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
		return
	}
	defer body.Close()

	contentType := mime.TypeByExtension(filepath.Ext(filename))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if _, err := io.Copy(w, body); err != nil {
		slog.ErrorContext(r.Context(), "failed to write image to response", "key", key, "err", err)
	}
}

// ---------------------------------------------------------------------------
// GET /db/status
// ---------------------------------------------------------------------------

func (h *handler) dbStatus(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, startup.Status())
}

// ---------------------------------------------------------------------------
// POST /db/refresh
// ---------------------------------------------------------------------------

func (h *handler) dbRefresh(w http.ResponseWriter, r *http.Request) {
	if err := startup.ForceRefresh(r.Context(), h.cfg.StartupCfg); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"status": "refreshed"})
}

// ---------------------------------------------------------------------------
// GET /templates/{monsterGameID}/apply/{templateGameID}
// ---------------------------------------------------------------------------

func (h *handler) applyTemplateByID(w http.ResponseWriter, r *http.Request) {
	monsterGameID := chi.URLParam(r, "monsterGameID")
	templateGameID := chi.URLParam(r, "templateGameID")

	d := db.Global()

	// Look up monster entry
	monsterEntry, err := db.GetByGameID(r.Context(), d, monsterGameID)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if monsterEntry == nil {
		jsonError(w, "monster not found", http.StatusNotFound)
		return
	}

	// Look up template entry, preferring the creature's edition
	tmplEntry, err := resolveTemplateEntry(r.Context(), d, templateGameID, monsterEntry.Edition)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if tmplEntry == nil {
		jsonError(w, "template not found", http.StatusNotFound)
		return
	}

	// Resolve S3 keys, honouring optional version query params
	monsterS3Key := resolveS3Key(r, monsterGameID, monsterEntry.S3Key, "monster_version")
	tmplS3Key := resolveS3Key(r, tmplEntry.GameID, tmplEntry.S3Key, "template_version")

	// Fetch JSON from S3
	creatureBytes, err := h.cfg.S3Client.GetObjectBytes(r.Context(), monsterS3Key)
	if err != nil {
		if isNotFound(err) {
			jsonError(w, "monster JSON not found in S3", http.StatusNotFound)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmplBytes, err := h.cfg.S3Client.GetObjectBytes(r.Context(), tmplS3Key)
	if err != nil {
		if isNotFound(err) {
			jsonError(w, "template JSON not found in S3", http.StatusNotFound)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var creature map[string]any
	if err := json.Unmarshal(creatureBytes, &creature); err != nil {
		jsonError(w, "invalid creature JSON", http.StatusInternalServerError)
		return
	}
	var tmpl template.TemplateJSON
	if err := json.Unmarshal(tmplBytes, &tmpl); err != nil {
		jsonError(w, "invalid template JSON", http.StatusInternalServerError)
		return
	}

	resp, err := template.Apply(creature, tmpl)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeTemplateResult(w, resp)
}

// ---------------------------------------------------------------------------
// POST /templates/apply
// ---------------------------------------------------------------------------

func (h *handler) applyTemplateInline(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Creature       map[string]any             `json:"creature"`
		TemplateGameID string                     `json:"template_game_id,omitempty"`
		TemplateVer    string                     `json:"template_version,omitempty"`
		Template       *template.TemplateJSON     `json:"template,omitempty"`
		Selections     []template.SelectionChoice `json:"selections,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.Creature == nil {
		jsonError(w, "creature is required", http.StatusBadRequest)
		return
	}

	var tmpl template.TemplateJSON

	if body.Template != nil {
		tmpl = *body.Template
	} else if body.TemplateGameID != "" {
		creatureEdition := creatureEditionFromJSON(body.Creature)
		fetched, err := h.fetchTemplateFromS3(w, r, body.TemplateGameID, body.TemplateVer, creatureEdition)
		if err != nil {
			return
		}
		tmpl = fetched
	} else {
		jsonError(w, "template or template_game_id is required", http.StatusBadRequest)
		return
	}

	resp, err := template.ApplyWithSelections(body.Creature, tmpl, body.Selections)
	if err != nil {
		if errors.Is(err, template.ErrBadSelection) {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeTemplateResult(w, resp)
}

// ---------------------------------------------------------------------------
// POST /defects — user-submitted defect report (wyrd pattern)
// ---------------------------------------------------------------------------

func (h *handler) reportDefect(w http.ResponseWriter, r *http.Request) {
	var report defects.Report
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&report); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := report.Validate(); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	client, err := defects.NewClient(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "defects client", "err", err)
		jsonError(w, "defect reporting is not configured", http.StatusServiceUnavailable)
		return
	}
	if err := client.Put(r.Context(), &report); err != nil {
		slog.ErrorContext(r.Context(), "defect put", "err", err)
		jsonError(w, "failed to store defect", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]any{"id": report.ID, "status": report.Status})
}

// resolveTemplateEntry looks up a template by game_id, and if the creature has
// a different edition, tries to find the same-edition alternate via the
// alternates table. Returns the best-match template entry.
func resolveTemplateEntry(ctx context.Context, d *sql.DB, templateGameID string, creatureEdition *string) (*db.Entry, error) {
	tmplEntry, err := db.GetByGameID(ctx, d, templateGameID)
	if err != nil {
		return nil, err
	}
	if tmplEntry == nil {
		return nil, nil
	}

	// If creature has an edition and the template is a different edition,
	// try to find the alternate template for the creature's edition.
	if creatureEdition != nil && tmplEntry.Edition != nil &&
		*creatureEdition != *tmplEntry.Edition {
		altGameID, err := db.GetAlternateGameID(ctx, d, templateGameID, *creatureEdition)
		if err != nil {
			return nil, err
		}
		if altGameID != "" {
			altEntry, err := db.GetByGameID(ctx, d, altGameID)
			if err != nil {
				return nil, err
			}
			if altEntry != nil {
				return altEntry, nil
			}
		}
		// No same-type alternate: the rules carrier may have changed TYPE
		// across editions (BotD vampire template <-> Monster Core vampire
		// family) — try the curated equivalents.
		equivGameID, err := db.GetEquivalentGameID(ctx, d, templateGameID, *creatureEdition)
		if err != nil {
			return nil, err
		}
		if equivGameID != "" {
			equivEntry, err := db.GetByGameID(ctx, d, equivGameID)
			if err != nil {
				return nil, err
			}
			if equivEntry != nil {
				return equivEntry, nil
			}
		}
	}

	return tmplEntry, nil
}

// fetchTemplateFromS3 looks up a template by game_id (with edition resolution),
// fetches its JSON from S3, and returns the parsed template. On error it writes
// the HTTP response and returns a non-nil error so the caller can return.
func (h *handler) fetchTemplateFromS3(w http.ResponseWriter, r *http.Request, gameID, wantVersion string, creatureEdition *string) (template.TemplateJSON, error) {
	var tmpl template.TemplateJSON
	d := db.Global()

	tmplEntry, err := resolveTemplateEntry(r.Context(), d, gameID, creatureEdition)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return tmpl, err
	}
	if tmplEntry == nil {
		slog.WarnContext(r.Context(), "template not found", "game_id", gameID)
		jsonError(w, "template not found", http.StatusNotFound)
		return tmpl, fmt.Errorf("template not found: %s", gameID)
	}

	s3Key := tmplEntry.S3Key
	if wantVersion != "" {
		versions, verErr := db.GetVersions(r.Context(), d, tmplEntry.GameID)
		if verErr != nil {
			slog.WarnContext(r.Context(), "failed to fetch template versions", "game_id", tmplEntry.GameID, "err", verErr)
		}
		for _, v := range versions {
			if v.SchemaVersion == wantVersion {
				s3Key = v.S3Key
				break
			}
		}
	}

	tmplBytes, err := h.cfg.S3Client.GetObjectBytes(r.Context(), s3Key)
	if err != nil {
		if isNotFound(err) {
			jsonError(w, "template JSON not found in S3", http.StatusNotFound)
		} else {
			jsonError(w, err.Error(), http.StatusInternalServerError)
		}
		return tmpl, err
	}
	if err := json.Unmarshal(tmplBytes, &tmpl); err != nil {
		jsonError(w, "invalid template JSON", http.StatusInternalServerError)
		return tmpl, err
	}
	return tmpl, nil
}

// creatureEditionFromJSON reads the "edition" field from an inline creature JSON.
func creatureEditionFromJSON(creature map[string]any) *string {
	if ed, ok := creature["edition"].(string); ok {
		return &ed
	}
	return nil
}

// resolveS3Key checks for a version query param and returns the appropriate S3 key.
func resolveS3Key(r *http.Request, gameID, defaultKey, queryParam string) string {
	wantVersion := r.URL.Query().Get(queryParam)
	if wantVersion == "" {
		return defaultKey
	}
	versions, err := db.GetVersions(r.Context(), db.Global(), gameID)
	if err != nil {
		slog.WarnContext(r.Context(), "resolveS3Key: failed to fetch versions", "game_id", gameID, "err", err)
	}
	for _, v := range versions {
		if v.SchemaVersion == wantVersion {
			return v.S3Key
		}
	}
	return defaultKey
}

// ---------------------------------------------------------------------------
// Multipart template response
// ---------------------------------------------------------------------------

// writeTemplateResult writes an ApplyResult as a multipart/mixed response with
// two JSON parts: the patch document and the modified creature.
func writeTemplateResult(w http.ResponseWriter, resp *template.ApplyResult) {
	mw := multipart.NewWriter(w)
	w.Header().Set("Content-Type", "multipart/mixed; boundary="+mw.Boundary())
	defer func() {
		if err := mw.Close(); err != nil {
			slog.Error("failed to close multipart writer", "err", err)
		}
	}()

	if err := writeJSONPart(mw, "patches", resp.PatchDoc); err != nil {
		slog.Error("failed to write patches part", "err", err)
		return
	}
	if err := writeJSONPart(mw, "creature", resp.Creature); err != nil {
		slog.Error("failed to write creature part", "err", err)
	}
}

// writeJSONPart writes a single JSON-encoded part to a multipart writer.
func writeJSONPart(mw *multipart.Writer, name string, v any) error {
	part, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":        {"application/json"},
		"Content-Disposition": {fmt.Sprintf("inline; name=%q", name)},
	})
	if err != nil {
		return fmt.Errorf("create part %s: %w", name, err)
	}
	if err := json.NewEncoder(part).Encode(v); err != nil {
		return fmt.Errorf("encode part %s: %w", name, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("jsonOK: failed to encode response", "err", err)
	}
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	if code >= http.StatusInternalServerError {
		slog.Error("server error", "code", code, "msg", msg)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		slog.Error("jsonError: failed to encode error response", "err", err)
	}
}

func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "NoSuchKey") ||
		strings.Contains(err.Error(), "404") ||
		strings.Contains(err.Error(), "not found")
}

// contextKey is used for request-scoped values (future use: auth, trace IDs).
type contextKey string
