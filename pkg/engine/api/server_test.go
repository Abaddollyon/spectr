package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Abaddollyon/spectr/pkg/engine"
)

// --------------------------------------------------------------------------
// mock store
// --------------------------------------------------------------------------

// mockStore is an in-memory engine.Store implementation for testing.
type mockStore struct {
	runs      []*engine.Run
	events    map[string][]*engine.Event   // keyed by runID
	toolCalls map[string][]*engine.ToolCall // keyed by runID
	alerts    []*engine.Alert
	buckets   []engine.TimeBucket
	freqs     []engine.ToolFrequency

	// control errors
	errGetRun    error
	errListRuns  error
	errAckAlert  error
	errListAlerts error
}

func newMockStore() *mockStore {
	now := time.Now().UTC()
	runID := "run-001"
	alertID := "alert-001"
	eventID := "evt-001"

	return &mockStore{
		runs: []*engine.Run{
			{
				ID:             runID,
				Framework:      "openclaw",
				AgentID:        "agent-1",
				SessionKey:     "session-abc",
				Status:         engine.RunStatusRunning,
				StartedAt:      now.Add(-5 * time.Minute),
				TotalTokensIn:  1000,
				TotalTokensOut: 500,
				TotalCostUSD:   0.012,
			},
		},
		events: map[string][]*engine.Event{
			runID: {
				{
					ID:        eventID,
					RunID:     runID,
					Type:      engine.EventTypeMessage,
					Timestamp: now,
					TokensIn:  100,
					TokensOut: 50,
					CostUSD:   0.001,
				},
			},
		},
		toolCalls: map[string][]*engine.ToolCall{
			runID: {
				{
					ID:         "tc-001",
					EventID:    eventID,
					RunID:      runID,
					Tool:       "bash",
					DurationMs: 120,
					Success:    true,
					Timestamp:  now,
				},
			},
		},
		alerts: []*engine.Alert{
			{
				ID:        alertID,
				Type:      engine.AlertTypeCostSpike,
				Severity:  engine.SeverityWarning,
				Message:   "cost spike detected",
				Metric:    "cost_usd",
				Expected:  0.01,
				Actual:    0.05,
				ZScore:    3.2,
				CreatedAt: now,
			},
		},
		buckets: []engine.TimeBucket{
			{Timestamp: now.Add(-1 * time.Hour), Value: 1500},
			{Timestamp: now, Value: 2000},
		},
		freqs: []engine.ToolFrequency{
			{Tool: "bash", Count: 42},
			{Tool: "read", Count: 30},
		},
	}
}

func (m *mockStore) InsertRun(_ context.Context, _ *engine.Run) error  { return nil }
func (m *mockStore) UpdateRun(_ context.Context, _ *engine.Run) error  { return nil }
func (m *mockStore) InsertEvent(_ context.Context, _ *engine.Event) error { return nil }
func (m *mockStore) InsertToolCall(_ context.Context, _ *engine.ToolCall) error { return nil }
func (m *mockStore) InsertAlert(_ context.Context, _ *engine.Alert) error { return nil }
func (m *mockStore) Close() error { return nil }

func (m *mockStore) GetRun(_ context.Context, id string) (*engine.Run, error) {
	if m.errGetRun != nil {
		return nil, m.errGetRun
	}
	for _, r := range m.runs {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, errors.New("not found")
}

func (m *mockStore) ListRuns(_ context.Context, _ engine.ListRunsOpts) ([]*engine.Run, error) {
	if m.errListRuns != nil {
		return nil, m.errListRuns
	}
	return m.runs, nil
}

func (m *mockStore) ListEvents(_ context.Context, runID string) ([]*engine.Event, error) {
	return m.events[runID], nil
}

func (m *mockStore) ListToolCalls(_ context.Context, runID string) ([]*engine.ToolCall, error) {
	return m.toolCalls[runID], nil
}

func (m *mockStore) ListAlerts(_ context.Context, _ engine.ListAlertsOpts) ([]*engine.Alert, error) {
	if m.errListAlerts != nil {
		return nil, m.errListAlerts
	}
	return m.alerts, nil
}

func (m *mockStore) AckAlert(_ context.Context, id string) error {
	if m.errAckAlert != nil {
		return m.errAckAlert
	}
	for _, a := range m.alerts {
		if a.ID == id {
			now := time.Now().UTC()
			a.AckedAt = &now
			return nil
		}
	}
	return errors.New("not found")
}

func (m *mockStore) TokenUsageOverTime(_ context.Context, _, _ time.Time, _ string) ([]engine.TimeBucket, error) {
	return m.buckets, nil
}

func (m *mockStore) CostOverTime(_ context.Context, _, _ time.Time, _ string) ([]engine.TimeBucket, error) {
	return m.buckets, nil
}

func (m *mockStore) TopToolCalls(_ context.Context, _, _ time.Time, _ int) ([]engine.ToolFrequency, error) {
	return m.freqs, nil
}

func (m *mockStore) GetCollectorState(_ context.Context, _ string) (*engine.CollectorState, error) {
	return nil, nil
}

func (m *mockStore) SetCollectorState(_ context.Context, _ *engine.CollectorState) error {
	return nil
}

// --------------------------------------------------------------------------
// test helpers
// --------------------------------------------------------------------------

func newTestServer(t *testing.T) *Server {
	t.Helper()
	store := newMockStore()
	s := NewServer(store, 0)
	go s.hub.run()
	return s
}

func doRequest(t *testing.T, s *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	return rr
}

func decodeJSON(t *testing.T, rr *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(rr.Body).Decode(v); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
}

// --------------------------------------------------------------------------
// health
// --------------------------------------------------------------------------

func TestHandleHealth(t *testing.T) {
	s := newTestServer(t)
	rr := doRequest(t, s, http.MethodGet, "/api/v1/health")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var body map[string]string
	decodeJSON(t, rr, &body)
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", body["status"])
	}
}

// --------------------------------------------------------------------------
// runs
// --------------------------------------------------------------------------

func TestHandleListRuns(t *testing.T) {
	s := newTestServer(t)
	rr := doRequest(t, s, http.MethodGet, "/api/v1/runs")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var runs []*engine.Run
	decodeJSON(t, rr, &runs)
	if len(runs) != 1 {
		t.Errorf("expected 1 run, got %d", len(runs))
	}
	if runs[0].ID != "run-001" {
		t.Errorf("unexpected run ID %q", runs[0].ID)
	}
}

func TestHandleListRuns_QueryParams(t *testing.T) {
	s := newTestServer(t)

	tests := []struct {
		name       string
		query      string
		wantStatus int
	}{
		{"framework filter", "/api/v1/runs?framework=openclaw", http.StatusOK},
		{"status filter", "/api/v1/runs?status=running", http.StatusOK},
		{"since filter", "/api/v1/runs?since=2024-01-01T00:00:00Z", http.StatusOK},
		{"limit filter", "/api/v1/runs?limit=10", http.StatusOK},
		{"invalid since", "/api/v1/runs?since=not-a-date", http.StatusBadRequest},
		{"invalid limit", "/api/v1/runs?limit=abc", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := doRequest(t, s, http.MethodGet, tc.query)
			if rr.Code != tc.wantStatus {
				t.Errorf("expected %d, got %d (body: %s)", tc.wantStatus, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestHandleListRuns_StoreError(t *testing.T) {
	s := newTestServer(t)
	s.store.(*mockStore).errListRuns = errors.New("db failure")

	rr := doRequest(t, s, http.MethodGet, "/api/v1/runs")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}

func TestHandleGetRun(t *testing.T) {
	s := newTestServer(t)
	rr := doRequest(t, s, http.MethodGet, "/api/v1/runs/run-001")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var body map[string]json.RawMessage
	decodeJSON(t, rr, &body)

	if _, ok := body["run"]; !ok {
		t.Error("expected 'run' key in response")
	}
	if _, ok := body["events"]; !ok {
		t.Error("expected 'events' key in response")
	}
}

func TestHandleGetRun_NotFound(t *testing.T) {
	s := newTestServer(t)
	rr := doRequest(t, s, http.MethodGet, "/api/v1/runs/does-not-exist")

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestHandleListEvents(t *testing.T) {
	s := newTestServer(t)
	rr := doRequest(t, s, http.MethodGet, "/api/v1/runs/run-001/events")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var events []*engine.Event
	decodeJSON(t, rr, &events)
	if len(events) != 1 {
		t.Errorf("expected 1 event, got %d", len(events))
	}
}

func TestHandleListEvents_UnknownRun(t *testing.T) {
	s := newTestServer(t)
	rr := doRequest(t, s, http.MethodGet, "/api/v1/runs/unknown/events")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 (empty list), got %d", rr.Code)
	}

	var events []*engine.Event
	decodeJSON(t, rr, &events)
	if len(events) != 0 {
		t.Errorf("expected empty events, got %d", len(events))
	}
}

func TestHandleListToolCalls(t *testing.T) {
	s := newTestServer(t)
	rr := doRequest(t, s, http.MethodGet, "/api/v1/runs/run-001/tools")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var tools []*engine.ToolCall
	decodeJSON(t, rr, &tools)
	if len(tools) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(tools))
	}
	if tools[0].Tool != "bash" {
		t.Errorf("expected tool=bash, got %q", tools[0].Tool)
	}
}

// --------------------------------------------------------------------------
// alerts
// --------------------------------------------------------------------------

func TestHandleListAlerts(t *testing.T) {
	s := newTestServer(t)
	rr := doRequest(t, s, http.MethodGet, "/api/v1/alerts")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var alerts []*engine.Alert
	decodeJSON(t, rr, &alerts)
	if len(alerts) != 1 {
		t.Errorf("expected 1 alert, got %d", len(alerts))
	}
}

func TestHandleListAlerts_QueryParams(t *testing.T) {
	s := newTestServer(t)

	tests := []struct {
		name       string
		query      string
		wantStatus int
	}{
		{"severity filter", "/api/v1/alerts?severity=warning", http.StatusOK},
		{"since filter", "/api/v1/alerts?since=2024-01-01T00:00:00Z", http.StatusOK},
		{"limit filter", "/api/v1/alerts?limit=5", http.StatusOK},
		{"invalid since", "/api/v1/alerts?since=bad-date", http.StatusBadRequest},
		{"invalid limit", "/api/v1/alerts?limit=xyz", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rr := doRequest(t, s, http.MethodGet, tc.query)
			if rr.Code != tc.wantStatus {
				t.Errorf("expected %d, got %d (body: %s)", tc.wantStatus, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestHandleAckAlert(t *testing.T) {
	s := newTestServer(t)
	rr := doRequest(t, s, http.MethodPost, "/api/v1/alerts/alert-001/ack")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}

	var body map[string]string
	decodeJSON(t, rr, &body)
	if body["status"] != "acked" {
		t.Errorf("expected status=acked, got %q", body["status"])
	}
}

func TestHandleAckAlert_NotFound(t *testing.T) {
	s := newTestServer(t)
	rr := doRequest(t, s, http.MethodPost, "/api/v1/alerts/no-such-alert/ack")

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

func TestHandleAckAlert_StoreError(t *testing.T) {
	s := newTestServer(t)
	s.store.(*mockStore).errAckAlert = errors.New("db error")

	rr := doRequest(t, s, http.MethodPost, "/api/v1/alerts/alert-001/ack")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on store error, got %d", rr.Code)
	}
}

// --------------------------------------------------------------------------
// stats
// --------------------------------------------------------------------------

func TestHandleTokensOverTime(t *testing.T) {
	s := newTestServer(t)
	rr := doRequest(t, s, http.MethodGet, "/api/v1/stats/tokens")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var buckets []engine.TimeBucket
	decodeJSON(t, rr, &buckets)
	if len(buckets) != 2 {
		t.Errorf("expected 2 buckets, got %d", len(buckets))
	}
}

func TestHandleTokensOverTime_WithParams(t *testing.T) {
	s := newTestServer(t)
	rr := doRequest(t, s, http.MethodGet, "/api/v1/stats/tokens?from=2024-01-01T00:00:00Z&to=2024-01-02T00:00:00Z&interval=30m")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", rr.Code, rr.Body.String())
	}
}

func TestHandleTokensOverTime_BadParam(t *testing.T) {
	s := newTestServer(t)
	rr := doRequest(t, s, http.MethodGet, "/api/v1/stats/tokens?from=not-a-date")

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestHandleCostOverTime(t *testing.T) {
	s := newTestServer(t)
	rr := doRequest(t, s, http.MethodGet, "/api/v1/stats/cost")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var buckets []engine.TimeBucket
	decodeJSON(t, rr, &buckets)
	if len(buckets) == 0 {
		t.Error("expected non-empty buckets")
	}
}

func TestHandleTopTools(t *testing.T) {
	s := newTestServer(t)
	rr := doRequest(t, s, http.MethodGet, "/api/v1/stats/tools")

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var freqs []engine.ToolFrequency
	decodeJSON(t, rr, &freqs)
	if len(freqs) != 2 {
		t.Errorf("expected 2 freqs, got %d", len(freqs))
	}
	if freqs[0].Tool != "bash" {
		t.Errorf("expected first tool=bash, got %q", freqs[0].Tool)
	}
}

func TestHandleTopTools_WithLimit(t *testing.T) {
	s := newTestServer(t)

	tests := []struct {
		query      string
		wantStatus int
	}{
		{"/api/v1/stats/tools?limit=5", http.StatusOK},
		{"/api/v1/stats/tools?limit=bad", http.StatusBadRequest},
	}

	for _, tc := range tests {
		rr := doRequest(t, s, http.MethodGet, tc.query)
		if rr.Code != tc.wantStatus {
			t.Errorf("query %s: expected %d, got %d", tc.query, tc.wantStatus, rr.Code)
		}
	}
}

// --------------------------------------------------------------------------
// content-type middleware
// --------------------------------------------------------------------------

func TestJSONContentType(t *testing.T) {
	s := newTestServer(t)
	rr := doRequest(t, s, http.MethodGet, "/api/v1/health")

	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

// --------------------------------------------------------------------------
// WebSocket
// --------------------------------------------------------------------------

func TestWebSocketConnect(t *testing.T) {
	s := newTestServer(t)

	// Spin up a real HTTP server so the WebSocket upgrade works.
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/stream"

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to connect to WebSocket: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	// Allow hub goroutine to register the client.
	time.Sleep(50 * time.Millisecond)

	if s.hub.ConnectedClients() != 1 {
		t.Errorf("expected 1 connected client, got %d", s.hub.ConnectedClients())
	}
}

func TestWebSocketBroadcast(t *testing.T) {
	s := newTestServer(t)

	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/stream"

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to connect to WebSocket: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	// Let the hub register the client.
	time.Sleep(50 * time.Millisecond)

	// Broadcast an event.
	now := time.Now().UTC()
	event := &engine.Event{
		ID:        "broadcast-evt-001",
		RunID:     "run-001",
		Type:      engine.EventTypeMessage,
		Timestamp: now,
	}
	if err := s.hub.BroadcastEvent(event); err != nil {
		t.Fatalf("BroadcastEvent: %v", err)
	}

	// Read from the WebSocket with a deadline.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read WebSocket message: %v", err)
	}

	var envelope wsEnvelope
	if err := json.Unmarshal(msg, &envelope); err != nil {
		t.Fatalf("failed to unmarshal envelope: %v", err)
	}
	if envelope.Type != "event" {
		t.Errorf("expected type=event, got %q", envelope.Type)
	}
	if envelope.Payload == nil {
		t.Fatal("expected non-nil payload")
	}
	if envelope.Payload.ID != "broadcast-evt-001" {
		t.Errorf("expected event ID broadcast-evt-001, got %q", envelope.Payload.ID)
	}
}

func TestWebSocketMultipleClients(t *testing.T) {
	s := newTestServer(t)

	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/stream"

	var conns []*websocket.Conn
	for i := 0; i < 3; i++ {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("client %d: failed to connect: %v", i, err)
		}
		conns = append(conns, conn)
		t.Cleanup(func() { conn.Close() })
	}

	time.Sleep(100 * time.Millisecond)

	if s.hub.ConnectedClients() != 3 {
		t.Errorf("expected 3 connected clients, got %d", s.hub.ConnectedClients())
	}

	event := &engine.Event{
		ID:        "multi-evt-001",
		RunID:     "run-001",
		Type:      engine.EventTypeToolCall,
		Timestamp: time.Now().UTC(),
	}
	if err := s.hub.BroadcastEvent(event); err != nil {
		t.Fatalf("BroadcastEvent: %v", err)
	}

	// All three clients should receive the message.
	for i, conn := range conns {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("client %d: failed to read: %v", i, err)
		}
		var env wsEnvelope
		if err := json.Unmarshal(msg, &env); err != nil {
			t.Fatalf("client %d: unmarshal: %v", i, err)
		}
		if env.Payload.ID != "multi-evt-001" {
			t.Errorf("client %d: expected event multi-evt-001, got %q", i, env.Payload.ID)
		}
	}
}
