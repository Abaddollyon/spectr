package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Abaddollyon/spectr/pkg/engine"
)

// --------------------------------------------------------------------------
// helpers
// --------------------------------------------------------------------------

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

// parseTimeRange extracts from/to/interval from query params.
// Defaults: from = now-24h, to = now, interval = "1h".
func parseTimeRange(r *http.Request) (from, to time.Time, interval string, err error) {
	q := r.URL.Query()
	from = time.Now().UTC().Add(-24 * time.Hour)
	to = time.Now().UTC()
	interval = "1h"

	if f := q.Get("from"); f != "" {
		from, err = time.Parse(time.RFC3339, f)
		if err != nil {
			return
		}
	}
	if t := q.Get("to"); t != "" {
		to, err = time.Parse(time.RFC3339, t)
		if err != nil {
			return
		}
	}
	if iv := q.Get("interval"); iv != "" {
		interval = iv
	}
	return
}

// --------------------------------------------------------------------------
// health
// --------------------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --------------------------------------------------------------------------
// runs
// --------------------------------------------------------------------------

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opts := engine.ListRunsOpts{}

	if fw := q.Get("framework"); fw != "" {
		opts.Framework = fw
	}
	if st := q.Get("status"); st != "" {
		opts.Status = engine.RunStatus(st)
	}
	if since := q.Get("since"); since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid 'since': use RFC3339 format")
			return
		}
		opts.Since = &t
	}
	if lim := q.Get("limit"); lim != "" {
		n, err := strconv.Atoi(lim)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid 'limit': must be a non-negative integer")
			return
		}
		opts.Limit = n
	}

	runs, err := s.store.ListRuns(r.Context(), opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if runs == nil {
		runs = []*engine.Run{}
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	run, err := s.store.GetRun(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	events, err := s.store.ListEvents(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if events == nil {
		events = []*engine.Event{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"run":    run,
		"events": events,
	})
}

func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	events, err := s.store.ListEvents(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if events == nil {
		events = []*engine.Event{}
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) handleListToolCalls(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	tools, err := s.store.ListToolCalls(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tools == nil {
		tools = []*engine.ToolCall{}
	}
	writeJSON(w, http.StatusOK, tools)
}

// --------------------------------------------------------------------------
// alerts
// --------------------------------------------------------------------------

func (s *Server) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opts := engine.ListAlertsOpts{}

	if sev := q.Get("severity"); sev != "" {
		opts.Severity = engine.Severity(sev)
	}
	if since := q.Get("since"); since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid 'since': use RFC3339 format")
			return
		}
		opts.Since = &t
	}
	if lim := q.Get("limit"); lim != "" {
		n, err := strconv.Atoi(lim)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid 'limit': must be a non-negative integer")
			return
		}
		opts.Limit = n
	}

	alerts, err := s.store.ListAlerts(r.Context(), opts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if alerts == nil {
		alerts = []*engine.Alert{}
	}
	writeJSON(w, http.StatusOK, alerts)
}

func (s *Server) handleAckAlert(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := s.store.AckAlert(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "alert not found or already acknowledged")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "acked"})
}

// --------------------------------------------------------------------------
// stats
// --------------------------------------------------------------------------

func (s *Server) handleTokensOverTime(w http.ResponseWriter, r *http.Request) {
	from, to, interval, err := parseTimeRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid time parameter: use RFC3339 format")
		return
	}

	buckets, err := s.store.TokenUsageOverTime(r.Context(), from, to, interval)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if buckets == nil {
		buckets = []engine.TimeBucket{}
	}
	writeJSON(w, http.StatusOK, buckets)
}

func (s *Server) handleCostOverTime(w http.ResponseWriter, r *http.Request) {
	from, to, interval, err := parseTimeRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid time parameter: use RFC3339 format")
		return
	}

	buckets, err := s.store.CostOverTime(r.Context(), from, to, interval)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if buckets == nil {
		buckets = []engine.TimeBucket{}
	}
	writeJSON(w, http.StatusOK, buckets)
}

func (s *Server) handleTopTools(w http.ResponseWriter, r *http.Request) {
	from, to, _, err := parseTimeRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid time parameter: use RFC3339 format")
		return
	}

	limit := 10
	if l := r.URL.Query().Get("limit"); l != "" {
		limit, err = strconv.Atoi(l)
		if err != nil || limit < 0 {
			writeError(w, http.StatusBadRequest, "invalid 'limit': must be a non-negative integer")
			return
		}
	}

	freqs, err := s.store.TopToolCalls(r.Context(), from, to, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if freqs == nil {
		freqs = []engine.ToolFrequency{}
	}
	writeJSON(w, http.StatusOK, freqs)
}
