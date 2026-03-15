// Package api implements the HTTP handlers for the pfsrd2 data API.
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/521studios/pfsrd2-data-api/internal/db"
	"github.com/521studios/pfsrd2-data-api/internal/s3"
	"github.com/521studios/pfsrd2-data-api/internal/startup"
)

// Config holds handler dependencies injected at startup.
type Config struct {
	S3Client    *s3.Client
	ImageDomain string // app domain for image redirects, e.g. "lets-roll.org"
	StartupCfg  startup.Config
}

// ---------------------------------------------------------------------------
// Router
// ---------------------------------------------------------------------------

func NewRouter(cfg Config) *chi.Mux {
	r := chi.NewRouter()
	h := &handler{cfg: cfg}

	r.Route("/api/pfsrd2", func(r chi.Router) {
		r.Get("/search", h.search)
		r.Get("/search/suggest", h.suggest)
		r.Get("/types", h.types)
		r.Get("/sources", h.sources)
		r.Get("/entries/{gameID}", h.getEntry)
		r.Get("/entries/{gameID}/full", h.getEntryFull)
		r.Get("/images/{category}/{filename}", h.imageRedirect)
		r.Get("/db/status", h.dbStatus)
		r.Post("/db/refresh", h.dbRefresh)

		// /{type} — paginated list
		// /{type}/{schema_version}/{book}/{filename} — full JSON from S3
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
		Q:       r.URL.Query().Get("q"),
		Type:    r.URL.Query().Get("type"),
		Level:   r.URL.Query().Get("level"),
		Source:  r.URL.Query().Get("source"),
		Edition: r.URL.Query().Get("edition"),
		Traits:  r.URL.Query().Get("traits"),
		Limit:   queryInt(r, "limit", 20),
		Offset:  queryInt(r, "offset", 0),
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
	if len(q) < 3 {
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
		Type:    chi.URLParam(r, "type"),
		Source:  r.URL.Query().Get("source"),
		Edition: r.URL.Query().Get("edition"),
		Level:   r.URL.Query().Get("level"),
		Limit:   queryInt(r, "limit", 20),
		Offset:  queryInt(r, "offset", 0),
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
	w.Write(body)
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
	// Include available versions
	versions, _ := db.GetVersions(r.Context(), db.Global(), gameID)
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
		versions, _ := db.GetVersions(r.Context(), db.Global(), gameID)
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
	w.Write(body)
}

// ---------------------------------------------------------------------------
// GET /images/{category}/{filename}
// ---------------------------------------------------------------------------

func (h *handler) imageRedirect(w http.ResponseWriter, r *http.Request) {
	category := chi.URLParam(r, "category")
	filename := chi.URLParam(r, "filename")
	url := "https://" + h.cfg.ImageDomain + "/pfsrd2/images/" + category + "/" + filename
	http.Redirect(w, r, url, http.StatusFound)
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
// Helpers
// ---------------------------------------------------------------------------

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
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
