package server

import (
	"context"
	"encoding/json"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kubelogs/kubelogs/internal/storage"
	"github.com/kubelogs/kubelogs/internal/web"
)

// HTTPServer serves the web UI.
type HTTPServer struct {
	store     storage.Store
	templates *template.Template
	staticFS  fs.FS
}

// NewHTTPServer creates a new HTTP server for the web UI.
func NewHTTPServer(store storage.Store) (*HTTPServer, error) {
	tmpl, err := web.Templates()
	if err != nil {
		return nil, err
	}

	staticFS, err := web.StaticFS()
	if err != nil {
		return nil, err
	}

	return &HTTPServer{
		store:     store,
		templates: tmpl,
		staticFS:  staticFS,
	}, nil
}

// Routes returns the HTTP handler with all routes configured.
func (s *HTTPServer) Routes() http.Handler {
	mux := http.NewServeMux()

	// Static files
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(s.staticFS))))

	// Pages
	mux.HandleFunc("GET /", s.handleIndex)

	// API endpoints
	mux.HandleFunc("GET /api/logs", s.handleQueryLogs)
	mux.HandleFunc("GET /api/logs/stream", s.handleLogStream)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/filters/namespaces", s.handleListNamespaces)
	mux.HandleFunc("GET /api/filters/containers", s.handleListContainers)

	return s.withLogging(mux)
}

// withLogging wraps a handler with request logging.
func (s *HTTPServer) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Debug("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(start),
		)
	})
}

// handleIndex serves the main UI page.
func (s *HTTPServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "index.html", nil); err != nil {
		slog.Error("template error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// logEntryJSON is the JSON representation of a log entry for the API.
type logEntryJSON struct {
	ID        int64             `json:"id"`
	Timestamp int64             `json:"timestamp"` // Unix nanoseconds
	Namespace string            `json:"namespace"`
	Pod       string            `json:"pod"`
	Container string            `json:"container"`
	Severity  int               `json:"severity"`
	Message   string            `json:"message"`
	Attrs     map[string]string `json:"attrs,omitempty"`
}

// queryResponse is the JSON response for log queries.
type queryResponse struct {
	Entries    []logEntryJSON `json:"entries"`
	HasMore    bool           `json:"hasMore"`
	NextCursor int64          `json:"nextCursor,omitempty"`
	Total      int64          `json:"total,omitempty"`
}

// toJSON converts a storage LogEntry to JSON representation.
func toJSON(e storage.LogEntry) logEntryJSON {
	return logEntryJSON{
		ID:        e.ID,
		Timestamp: e.Timestamp.UnixNano(),
		Namespace: e.Namespace,
		Pod:       e.Pod,
		Container: e.Container,
		Severity:  int(e.Severity),
		Message:   e.Message,
		Attrs:     e.Attributes,
	}
}

// handleQueryLogs returns log entries matching the query parameters.
func (s *HTTPServer) handleQueryLogs(w http.ResponseWriter, r *http.Request) {
	q := s.parseQueryParams(r)

	result, err := s.store.Query(r.Context(), q)
	if err != nil {
		slog.Error("query error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	entries := make([]logEntryJSON, 0, len(result.Entries))
	for _, e := range result.Entries {
		entries = append(entries, toJSON(e))
	}

	resp := queryResponse{
		Entries:    entries,
		HasMore:    result.HasMore,
		NextCursor: result.NextCursor,
		Total:      result.TotalEstimate,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("json encode error", "error", err)
	}
}

// parseQueryParams extracts query parameters into a storage.Query.
func (s *HTTPServer) parseQueryParams(r *http.Request) storage.Query {
	q := storage.Query{
		Pagination: storage.Pagination{
			Limit: 100,
			Order: storage.OrderDesc,
		},
	}

	params := r.URL.Query()

	if v := params.Get("namespace"); v != "" {
		q.Namespace = v
	}
	if v := params.Get("pod"); v != "" {
		q.Pod = v
	}
	if v := params.Get("container"); v != "" {
		q.Container = v
	}
	if v := params.Get("search"); v != "" {
		q.Search = v
	}
	if v := params.Get("minSeverity"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 6 {
			q.MinSeverity = storage.Severity(n)
		}
	}
	if v := params.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			q.Pagination.Limit = n
		}
	}
	if v := params.Get("afterId"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			q.Pagination.AfterID = n
		}
	}
	if v := params.Get("beforeId"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			q.Pagination.BeforeID = n
		}
	}
	if v := params.Get("order"); v == "asc" {
		q.Pagination.Order = storage.OrderAsc
	}

	// Time range filtering
	if v := params.Get("startTime"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.StartTime = t
		}
	}
	if v := params.Get("endTime"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			q.EndTime = t
		}
	}

	// Attribute filters (attr.key=value format)
	for key, values := range params {
		if strings.HasPrefix(key, "attr.") && len(values) > 0 {
			if q.Attributes == nil {
				q.Attributes = make(map[string]string)
			}
			attrKey := strings.TrimPrefix(key, "attr.")
			q.Attributes[attrKey] = values[0]
		}
	}

	return q
}

// statsResponse is the JSON response for stats.
type statsResponse struct {
	TotalEntries  int64  `json:"totalEntries"`
	DiskSizeBytes int64  `json:"diskSizeBytes"`
	OldestEntry   string `json:"oldestEntry,omitempty"`
	NewestEntry   string `json:"newestEntry,omitempty"`
}

// handleStats returns storage statistics.
func (s *HTTPServer) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.Stats(r.Context())
	if err != nil {
		slog.Error("stats error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	resp := statsResponse{
		TotalEntries:  stats.TotalEntries,
		DiskSizeBytes: stats.DiskSizeBytes,
	}
	if !stats.OldestEntry.IsZero() {
		resp.OldestEntry = stats.OldestEntry.Format(time.RFC3339)
	}
	if !stats.NewestEntry.IsZero() {
		resp.NewestEntry = stats.NewestEntry.Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("json encode error", "error", err)
	}
}

// FilterLister is an interface for stores that can list filter values.
type FilterLister interface {
	ListNamespaces(ctx context.Context) ([]string, error)
	ListContainers(ctx context.Context) ([]string, error)
}

// handleListNamespaces returns distinct namespace values.
func (s *HTTPServer) handleListNamespaces(w http.ResponseWriter, r *http.Request) {
	lister, ok := s.store.(FilterLister)
	if !ok {
		http.Error(w, "Not supported", http.StatusNotImplemented)
		return
	}

	namespaces, err := lister.ListNamespaces(r.Context())
	if err != nil {
		slog.Error("list namespaces error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(namespaces); err != nil {
		slog.Error("json encode error", "error", err)
	}
}

// handleListContainers returns distinct container values.
func (s *HTTPServer) handleListContainers(w http.ResponseWriter, r *http.Request) {
	lister, ok := s.store.(FilterLister)
	if !ok {
		http.Error(w, "Not supported", http.StatusNotImplemented)
		return
	}

	containers, err := lister.ListContainers(r.Context())
	if err != nil {
		slog.Error("list containers error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(containers); err != nil {
		slog.Error("json encode error", "error", err)
	}
}
