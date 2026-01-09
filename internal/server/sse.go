package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/kubelogs/kubelogs/internal/storage"
)

// handleLogStream streams log entries via Server-Sent Events.
func (s *HTTPServer) handleLogStream(w http.ResponseWriter, r *http.Request) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Parse filter parameters
	filters := s.parseSSEFilters(r)

	// Get initial cursor - start from the most recent entries
	var lastID int64
	initialResult, err := s.store.Query(r.Context(), storage.Query{
		Namespace:   filters.namespace,
		Container:   filters.container,
		MinSeverity: filters.minSeverity,
		Pagination: storage.Pagination{
			Limit: 50,
			Order: storage.OrderDesc,
		},
	})
	if err == nil && len(initialResult.Entries) > 0 {
		// Send initial batch in reverse order (oldest first)
		for i := len(initialResult.Entries) - 1; i >= 0; i-- {
			entry := initialResult.Entries[i]
			s.sendSSEEvent(w, entry)
			lastID = entry.ID
		}
		flusher.Flush()
	}

	// Poll for new entries
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			q := storage.Query{
				Namespace:   filters.namespace,
				Container:   filters.container,
				MinSeverity: filters.minSeverity,
				Pagination: storage.Pagination{
					Limit:   100,
					AfterID: lastID,
					Order:   storage.OrderAsc,
				},
			}

			result, err := s.store.Query(r.Context(), q)
			if err != nil {
				slog.Debug("sse query error", "error", err)
				continue
			}

			for _, entry := range result.Entries {
				s.sendSSEEvent(w, entry)
				lastID = entry.ID
			}

			if len(result.Entries) > 0 {
				flusher.Flush()
			}
		}
	}
}

// sseFilters holds parsed SSE filter parameters.
type sseFilters struct {
	namespace   string
	container   string
	minSeverity storage.Severity
}

// parseSSEFilters extracts filter parameters from the request.
func (s *HTTPServer) parseSSEFilters(r *http.Request) sseFilters {
	params := r.URL.Query()
	filters := sseFilters{}

	filters.namespace = params.Get("namespace")
	filters.container = params.Get("container")

	if v := params.Get("minSeverity"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 6 {
			filters.minSeverity = storage.Severity(n)
		}
	}

	return filters
}

// sendSSEEvent sends a single log entry as an SSE event.
func (s *HTTPServer) sendSSEEvent(w http.ResponseWriter, entry storage.LogEntry) {
	data, err := json.Marshal(toJSON(entry))
	if err != nil {
		slog.Debug("sse marshal error", "error", err)
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
}
