package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Abaddollyon/spectr/pkg/engine"
)

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────

func newMemStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New(:memory:): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newFileStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := New(path)
	if err != nil {
		t.Fatalf("New(file): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func ptr[T any](v T) *T { return &v }

var ctx = context.Background()

// ─────────────────────────────────────────────────────────────
// WAL mode
// ─────────────────────────────────────────────────────────────

func TestWALMode(t *testing.T) {
	s := newFileStore(t)
	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("want journal_mode=wal, got %q", mode)
	}
}

func TestForeignKeysEnabled(t *testing.T) {
	s := newFileStore(t)
	var fk int
	if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("want foreign_keys=1, got %d", fk)
	}
}

// ─────────────────────────────────────────────────────────────
// Migrations (idempotency)
// ─────────────────────────────────────────────────────────────

func TestMigrationsIdempotent(t *testing.T) {
	s := newMemStore(t)
	// Running migrations a second time must not error.
	if err := runMigrations(s.db); err != nil {
		t.Fatalf("second runMigrations: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// Run CRUD
// ─────────────────────────────────────────────────────────────

func sampleRun() *engine.Run {
	return &engine.Run{
		Framework:  "openclaw",
		AgentID:    "agent-1",
		SessionKey: "sess-abc",
		Status:     engine.RunStatusRunning,
		StartedAt:  time.Now().UTC().Truncate(time.Millisecond),
	}
}

func TestInsertAndGetRun(t *testing.T) {
	s := newMemStore(t)
	r := sampleRun()

	if err := s.InsertRun(ctx, r); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}
	if r.ID == "" {
		t.Fatal("ID should be set after InsertRun")
	}

	got, err := s.GetRun(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.ID != r.ID {
		t.Errorf("ID mismatch: want %s got %s", r.ID, got.ID)
	}
	if got.Framework != r.Framework {
		t.Errorf("Framework mismatch: want %s got %s", r.Framework, got.Framework)
	}
	if got.Status != r.Status {
		t.Errorf("Status mismatch: want %s got %s", r.Status, got.Status)
	}
	if !got.StartedAt.Equal(r.StartedAt) {
		t.Errorf("StartedAt mismatch: want %v got %v", r.StartedAt, got.StartedAt)
	}
	if got.ParentRunID != nil {
		t.Errorf("ParentRunID should be nil")
	}
	if got.CompletedAt != nil {
		t.Errorf("CompletedAt should be nil")
	}
}

func TestInsertRunWithOptionalFields(t *testing.T) {
	s := newMemStore(t)
	parent := sampleRun()
	if err := s.InsertRun(ctx, parent); err != nil {
		t.Fatal(err)
	}

	completed := time.Now().UTC().Truncate(time.Millisecond)
	child := &engine.Run{
		ParentRunID:     &parent.ID,
		Framework:       "claudecode",
		AgentID:         "child-1",
		SessionKey:      "child-sess",
		Status:          engine.RunStatusCompleted,
		StartedAt:       time.Now().UTC().Truncate(time.Millisecond),
		CompletedAt:     &completed,
		TotalTokensIn:   100,
		TotalTokensOut:  200,
		TotalCostUSD:    0.05,
		Metadata:        map[string]string{"key": "value"},
	}
	if err := s.InsertRun(ctx, child); err != nil {
		t.Fatalf("InsertRun child: %v", err)
	}

	got, err := s.GetRun(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetRun child: %v", err)
	}
	if got.ParentRunID == nil || *got.ParentRunID != parent.ID {
		t.Errorf("ParentRunID mismatch")
	}
	if got.CompletedAt == nil || !got.CompletedAt.Equal(completed) {
		t.Errorf("CompletedAt mismatch: want %v got %v", completed, got.CompletedAt)
	}
	if got.TotalTokensIn != 100 {
		t.Errorf("TotalTokensIn: want 100 got %d", got.TotalTokensIn)
	}
	if got.Metadata["key"] != "value" {
		t.Errorf("Metadata mismatch")
	}
}

func TestUpdateRun(t *testing.T) {
	s := newMemStore(t)
	r := sampleRun()
	if err := s.InsertRun(ctx, r); err != nil {
		t.Fatal(err)
	}

	completed := time.Now().UTC().Truncate(time.Millisecond)
	r.Status = engine.RunStatusCompleted
	r.CompletedAt = &completed
	r.TotalTokensIn = 500
	r.TotalTokensOut = 750
	r.TotalCostUSD = 0.10

	if err := s.UpdateRun(ctx, r); err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}

	got, err := s.GetRun(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != engine.RunStatusCompleted {
		t.Errorf("Status: want completed got %s", got.Status)
	}
	if got.TotalTokensIn != 500 {
		t.Errorf("TotalTokensIn: want 500 got %d", got.TotalTokensIn)
	}
	if got.CompletedAt == nil || !got.CompletedAt.Equal(completed) {
		t.Errorf("CompletedAt mismatch")
	}
}

func TestGetRun_NotFound(t *testing.T) {
	s := newMemStore(t)
	_, err := s.GetRun(ctx, "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for missing run")
	}
}

func TestListRuns_All(t *testing.T) {
	s := newMemStore(t)
	for i := 0; i < 3; i++ {
		r := sampleRun()
		if err := s.InsertRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	runs, err := s.ListRuns(ctx, engine.ListRunsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Errorf("want 3 runs, got %d", len(runs))
	}
}

func TestListRuns_FilterFramework(t *testing.T) {
	s := newMemStore(t)
	r1 := sampleRun()
	r1.Framework = "openclaw"
	r2 := sampleRun()
	r2.Framework = "claudecode"
	_ = s.InsertRun(ctx, r1)
	_ = s.InsertRun(ctx, r2)

	runs, err := s.ListRuns(ctx, engine.ListRunsOpts{Framework: "openclaw"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Framework != "openclaw" {
		t.Errorf("framework filter failed: got %d runs", len(runs))
	}
}

func TestListRuns_FilterStatus(t *testing.T) {
	s := newMemStore(t)
	r1 := sampleRun()
	r1.Status = engine.RunStatusRunning
	r2 := sampleRun()
	r2.Status = engine.RunStatusCompleted
	_ = s.InsertRun(ctx, r1)
	_ = s.InsertRun(ctx, r2)

	runs, err := s.ListRuns(ctx, engine.ListRunsOpts{Status: engine.RunStatusCompleted})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != engine.RunStatusCompleted {
		t.Errorf("status filter failed")
	}
}

func TestListRuns_FilterSince(t *testing.T) {
	s := newMemStore(t)
	old := sampleRun()
	old.StartedAt = time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Millisecond)
	recent := sampleRun()
	recent.StartedAt = time.Now().UTC().Truncate(time.Millisecond)
	_ = s.InsertRun(ctx, old)
	_ = s.InsertRun(ctx, recent)

	since := time.Now().Add(-1 * time.Hour)
	runs, err := s.ListRuns(ctx, engine.ListRunsOpts{Since: &since})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Errorf("since filter: want 1 run, got %d", len(runs))
	}
}

func TestListRuns_LimitOffset(t *testing.T) {
	s := newMemStore(t)
	for i := 0; i < 5; i++ {
		r := sampleRun()
		if err := s.InsertRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	runs, err := s.ListRuns(ctx, engine.ListRunsOpts{Limit: 2, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Errorf("limit/offset: want 2 runs, got %d", len(runs))
	}
}

func TestListRuns_Empty(t *testing.T) {
	s := newMemStore(t)
	runs, err := s.ListRuns(ctx, engine.ListRunsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if runs != nil {
		t.Errorf("expected nil slice for empty table, got %v", runs)
	}
}

// ─────────────────────────────────────────────────────────────
// Events
// ─────────────────────────────────────────────────────────────

func insertSampleRun(t *testing.T, s *Store) *engine.Run {
	t.Helper()
	r := sampleRun()
	if err := s.InsertRun(ctx, r); err != nil {
		t.Fatalf("insertSampleRun: %v", err)
	}
	return r
}

func TestInsertAndListEvents(t *testing.T) {
	s := newMemStore(t)
	r := insertSampleRun(t, s)

	e1 := &engine.Event{
		RunID:     r.ID,
		Type:      engine.EventTypeMessage,
		Timestamp: time.Now().UTC().Truncate(time.Millisecond),
		TokensIn:  50,
		TokensOut: 100,
		CostUSD:   0.001,
		Model:     "claude-sonnet",
	}
	if err := s.InsertEvent(ctx, e1); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	if e1.ID == "" {
		t.Fatal("Event ID should be set after insert")
	}

	e2 := &engine.Event{
		RunID:     r.ID,
		Type:      engine.EventTypeToolCall,
		Timestamp: time.Now().UTC().Truncate(time.Millisecond),
	}
	_ = s.InsertEvent(ctx, e2)

	events, err := s.ListEvents(ctx, r.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("want 2 events, got %d", len(events))
	}
	if events[0].TokensIn != 50 {
		t.Errorf("TokensIn mismatch: want 50 got %d", events[0].TokensIn)
	}
}

func TestInsertEvent_OptionalFields(t *testing.T) {
	s := newMemStore(t)
	r := insertSampleRun(t, s)

	parentEvt := &engine.Event{
		RunID:     r.ID,
		Type:      engine.EventTypeMessage,
		Timestamp: time.Now().UTC().Truncate(time.Millisecond),
	}
	_ = s.InsertEvent(ctx, parentEvt)

	dur := int64(123)
	child := &engine.Event{
		RunID:         r.ID,
		ParentEventID: &parentEvt.ID,
		Type:          engine.EventTypeSpawn,
		Timestamp:     time.Now().UTC().Truncate(time.Millisecond),
		DurationMs:    &dur,
		Data:          []byte(`{"key":"value"}`),
	}
	if err := s.InsertEvent(ctx, child); err != nil {
		t.Fatalf("InsertEvent child: %v", err)
	}

	events, err := s.ListEvents(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found *engine.Event
	for _, e := range events {
		if e.ID == child.ID {
			found = e
		}
	}
	if found == nil {
		t.Fatal("child event not found")
	}
	if found.ParentEventID == nil || *found.ParentEventID != parentEvt.ID {
		t.Errorf("ParentEventID mismatch")
	}
	if found.DurationMs == nil || *found.DurationMs != 123 {
		t.Errorf("DurationMs mismatch")
	}
	if string(found.Data) != `{"key":"value"}` {
		t.Errorf("Data mismatch: %s", found.Data)
	}
}

func TestListEvents_Empty(t *testing.T) {
	s := newMemStore(t)
	r := insertSampleRun(t, s)

	events, err := s.ListEvents(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if events != nil {
		t.Errorf("expected nil for empty events")
	}
}

// ─────────────────────────────────────────────────────────────
// Tool calls
// ─────────────────────────────────────────────────────────────

func insertSampleEvent(t *testing.T, s *Store, runID string) *engine.Event {
	t.Helper()
	e := &engine.Event{
		RunID:     runID,
		Type:      engine.EventTypeToolCall,
		Timestamp: time.Now().UTC().Truncate(time.Millisecond),
	}
	if err := s.InsertEvent(ctx, e); err != nil {
		t.Fatalf("insertSampleEvent: %v", err)
	}
	return e
}

func TestInsertAndListToolCalls(t *testing.T) {
	s := newMemStore(t)
	r := insertSampleRun(t, s)
	e := insertSampleEvent(t, s, r.ID)

	tc := &engine.ToolCall{
		EventID:    e.ID,
		RunID:      r.ID,
		Tool:       "web_search",
		ArgsHash:   "abc123",
		DurationMs: 250,
		Success:    true,
		Timestamp:  time.Now().UTC().Truncate(time.Millisecond),
	}
	if err := s.InsertToolCall(ctx, tc); err != nil {
		t.Fatalf("InsertToolCall: %v", err)
	}
	if tc.ID == "" {
		t.Fatal("ToolCall ID should be set")
	}

	tcs, err := s.ListToolCalls(ctx, r.ID)
	if err != nil {
		t.Fatalf("ListToolCalls: %v", err)
	}
	if len(tcs) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(tcs))
	}
	if tcs[0].Tool != "web_search" {
		t.Errorf("Tool mismatch: want web_search got %s", tcs[0].Tool)
	}
	if !tcs[0].Success {
		t.Errorf("Success should be true")
	}
	if tcs[0].DurationMs != 250 {
		t.Errorf("DurationMs: want 250 got %d", tcs[0].DurationMs)
	}
}

func TestInsertToolCall_FailedCall(t *testing.T) {
	s := newMemStore(t)
	r := insertSampleRun(t, s)
	e := insertSampleEvent(t, s, r.ID)

	tc := &engine.ToolCall{
		EventID:   e.ID,
		RunID:     r.ID,
		Tool:      "exec",
		Success:   false,
		Timestamp: time.Now().UTC().Truncate(time.Millisecond),
	}
	_ = s.InsertToolCall(ctx, tc)

	tcs, _ := s.ListToolCalls(ctx, r.ID)
	if len(tcs) != 1 || tcs[0].Success {
		t.Errorf("expected false Success for failed call")
	}
}

func TestListToolCalls_Empty(t *testing.T) {
	s := newMemStore(t)
	r := insertSampleRun(t, s)
	tcs, err := s.ListToolCalls(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tcs != nil {
		t.Errorf("expected nil for empty tool calls")
	}
}

// ─────────────────────────────────────────────────────────────
// Alerts
// ─────────────────────────────────────────────────────────────

func sampleAlert() *engine.Alert {
	return &engine.Alert{
		Type:      engine.AlertTypeCostSpike,
		Severity:  engine.SeverityWarning,
		Message:   "cost exceeded threshold",
		Metric:    "cost_usd",
		Expected:  1.0,
		Actual:    5.0,
		ZScore:    3.2,
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
}

func TestInsertAndListAlerts(t *testing.T) {
	s := newMemStore(t)
	a := sampleAlert()
	if err := s.InsertAlert(ctx, a); err != nil {
		t.Fatalf("InsertAlert: %v", err)
	}
	if a.ID == "" {
		t.Fatal("Alert ID should be set")
	}

	alerts, err := s.ListAlerts(ctx, engine.ListAlertsOpts{})
	if err != nil {
		t.Fatalf("ListAlerts: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("want 1 alert, got %d", len(alerts))
	}
	got := alerts[0]
	if got.Metric != "cost_usd" {
		t.Errorf("Metric mismatch")
	}
	if got.Expected != 1.0 || got.Actual != 5.0 {
		t.Errorf("Expected/Actual mismatch")
	}
	if got.AckedAt != nil {
		t.Errorf("AckedAt should be nil")
	}
}

func TestInsertAlert_WithRunID(t *testing.T) {
	s := newMemStore(t)
	r := insertSampleRun(t, s)

	a := sampleAlert()
	a.RunID = &r.ID
	_ = s.InsertAlert(ctx, a)

	alerts, _ := s.ListAlerts(ctx, engine.ListAlertsOpts{})
	if len(alerts) != 1 || alerts[0].RunID == nil || *alerts[0].RunID != r.ID {
		t.Errorf("RunID mismatch on alert")
	}
}

func TestListAlerts_FilterSeverity(t *testing.T) {
	s := newMemStore(t)
	w := sampleAlert()
	w.Severity = engine.SeverityWarning
	_ = s.InsertAlert(ctx, w)

	c := sampleAlert()
	c.Severity = engine.SeverityCritical
	_ = s.InsertAlert(ctx, c)

	alerts, err := s.ListAlerts(ctx, engine.ListAlertsOpts{Severity: engine.SeverityCritical})
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 || alerts[0].Severity != engine.SeverityCritical {
		t.Errorf("severity filter failed")
	}
}

func TestListAlerts_FilterAckedOnly(t *testing.T) {
	s := newMemStore(t)
	a1 := sampleAlert()
	a2 := sampleAlert()
	_ = s.InsertAlert(ctx, a1)
	_ = s.InsertAlert(ctx, a2)
	_ = s.AckAlert(ctx, a1.ID)

	alerts, err := s.ListAlerts(ctx, engine.ListAlertsOpts{AckedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 || alerts[0].ID != a1.ID {
		t.Errorf("AckedOnly filter: want 1 acked alert, got %d", len(alerts))
	}
}

func TestListAlerts_FilterSince(t *testing.T) {
	s := newMemStore(t)
	old := sampleAlert()
	old.CreatedAt = time.Now().Add(-48 * time.Hour).UTC().Truncate(time.Millisecond)
	recent := sampleAlert()
	recent.CreatedAt = time.Now().UTC().Truncate(time.Millisecond)
	_ = s.InsertAlert(ctx, old)
	_ = s.InsertAlert(ctx, recent)

	since := time.Now().Add(-1 * time.Hour)
	alerts, err := s.ListAlerts(ctx, engine.ListAlertsOpts{Since: &since})
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 {
		t.Errorf("since filter: want 1 alert, got %d", len(alerts))
	}
}

func TestListAlerts_Limit(t *testing.T) {
	s := newMemStore(t)
	for i := 0; i < 5; i++ {
		a := sampleAlert()
		_ = s.InsertAlert(ctx, a)
	}
	alerts, err := s.ListAlerts(ctx, engine.ListAlertsOpts{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 3 {
		t.Errorf("limit: want 3, got %d", len(alerts))
	}
}

func TestAckAlert(t *testing.T) {
	s := newMemStore(t)
	a := sampleAlert()
	_ = s.InsertAlert(ctx, a)

	if err := s.AckAlert(ctx, a.ID); err != nil {
		t.Fatalf("AckAlert: %v", err)
	}

	alerts, _ := s.ListAlerts(ctx, engine.ListAlertsOpts{})
	if len(alerts) != 1 || alerts[0].AckedAt == nil {
		t.Errorf("AckedAt should be set after AckAlert")
	}
	if time.Since(*alerts[0].AckedAt) > 5*time.Second {
		t.Errorf("AckedAt too far in the past: %v", alerts[0].AckedAt)
	}
}

func TestListAlerts_Empty(t *testing.T) {
	s := newMemStore(t)
	alerts, err := s.ListAlerts(ctx, engine.ListAlertsOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if alerts != nil {
		t.Errorf("expected nil for empty alerts")
	}
}

// ─────────────────────────────────────────────────────────────
// Collector state
// ─────────────────────────────────────────────────────────────

func TestSetAndGetCollectorState(t *testing.T) {
	s := newMemStore(t)
	state := &engine.CollectorState{
		CollectorID: "openclaw",
		FilePath:    "/tmp/sessions.jsonl",
		ByteOffset:  1024,
		LastEventID: "evt-123",
	}

	if err := s.SetCollectorState(ctx, state); err != nil {
		t.Fatalf("SetCollectorState: %v", err)
	}

	got, err := s.GetCollectorState(ctx, "openclaw")
	if err != nil {
		t.Fatalf("GetCollectorState: %v", err)
	}
	if got == nil {
		t.Fatal("expected state, got nil")
	}
	if got.ByteOffset != 1024 {
		t.Errorf("ByteOffset: want 1024 got %d", got.ByteOffset)
	}
	if got.LastEventID != "evt-123" {
		t.Errorf("LastEventID mismatch")
	}
	if got.FilePath != "/tmp/sessions.jsonl" {
		t.Errorf("FilePath mismatch")
	}
}

func TestSetCollectorState_Upsert(t *testing.T) {
	s := newMemStore(t)
	state := &engine.CollectorState{
		CollectorID: "openclaw",
		FilePath:    "/tmp/file.jsonl",
		ByteOffset:  100,
		LastEventID: "evt-1",
	}
	_ = s.SetCollectorState(ctx, state)

	state.ByteOffset = 500
	state.LastEventID = "evt-5"
	if err := s.SetCollectorState(ctx, state); err != nil {
		t.Fatalf("upsert SetCollectorState: %v", err)
	}

	got, _ := s.GetCollectorState(ctx, "openclaw")
	if got.ByteOffset != 500 {
		t.Errorf("after upsert ByteOffset: want 500 got %d", got.ByteOffset)
	}
	if got.LastEventID != "evt-5" {
		t.Errorf("after upsert LastEventID mismatch")
	}
}

func TestGetCollectorState_NotFound(t *testing.T) {
	s := newMemStore(t)
	got, err := s.GetCollectorState(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing collector state")
	}
}

// ─────────────────────────────────────────────────────────────
// Aggregations
// ─────────────────────────────────────────────────────────────

func TestTokenUsageOverTime(t *testing.T) {
	s := newMemStore(t)
	r := insertSampleRun(t, s)

	now := time.Now().UTC().Truncate(time.Hour)
	for i := 0; i < 3; i++ {
		e := &engine.Event{
			RunID:     r.ID,
			Type:      engine.EventTypeMessage,
			Timestamp: now.Add(time.Duration(i) * 10 * time.Minute),
			TokensIn:  100,
			TokensOut: 200,
			CostUSD:   0.01,
		}
		_ = s.InsertEvent(ctx, e)
	}

	from := now
	to := now.Add(2 * time.Hour)
	buckets, err := s.TokenUsageOverTime(ctx, from, to, "1h")
	if err != nil {
		t.Fatalf("TokenUsageOverTime: %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("want 2 buckets, got %d", len(buckets))
	}
	// All 3 events fall in the first hour bucket: 3 * (100+200) = 900
	if buckets[0].Value != 900 {
		t.Errorf("bucket[0].Value: want 900 got %f", buckets[0].Value)
	}
	if buckets[1].Value != 0 {
		t.Errorf("bucket[1].Value: want 0 got %f", buckets[1].Value)
	}
}

func TestCostOverTime(t *testing.T) {
	s := newMemStore(t)
	r := insertSampleRun(t, s)

	now := time.Now().UTC().Truncate(time.Hour)
	for i := 0; i < 2; i++ {
		e := &engine.Event{
			RunID:     r.ID,
			Type:      engine.EventTypeMessage,
			Timestamp: now.Add(time.Duration(i) * 10 * time.Minute),
			CostUSD:   0.05,
		}
		_ = s.InsertEvent(ctx, e)
	}

	buckets, err := s.CostOverTime(ctx, now, now.Add(2*time.Hour), "1h")
	if err != nil {
		t.Fatalf("CostOverTime: %v", err)
	}
	if len(buckets) != 2 {
		t.Fatalf("want 2 buckets, got %d", len(buckets))
	}
	if buckets[0].Value != 0.10 {
		t.Errorf("bucket[0] cost: want 0.10 got %f", buckets[0].Value)
	}
}

func TestTopToolCalls(t *testing.T) {
	s := newMemStore(t)
	r := insertSampleRun(t, s)
	e := insertSampleEvent(t, s, r.ID)

	now := time.Now().UTC()
	tools := []struct {
		name  string
		count int
	}{
		{"web_search", 5},
		{"read_file", 3},
		{"exec", 1},
	}

	for _, tool := range tools {
		for i := 0; i < tool.count; i++ {
			tc := &engine.ToolCall{
				EventID:   e.ID,
				RunID:     r.ID,
				Tool:      tool.name,
				Timestamp: now,
			}
			_ = s.InsertToolCall(ctx, tc)
		}
	}

	freqs, err := s.TopToolCalls(ctx, now.Add(-time.Minute), now.Add(time.Minute), 10)
	if err != nil {
		t.Fatalf("TopToolCalls: %v", err)
	}
	if len(freqs) != 3 {
		t.Fatalf("want 3 tools, got %d", len(freqs))
	}
	if freqs[0].Tool != "web_search" || freqs[0].Count != 5 {
		t.Errorf("top tool: want web_search(5), got %s(%d)", freqs[0].Tool, freqs[0].Count)
	}
}

func TestTopToolCalls_DefaultLimit(t *testing.T) {
	s := newMemStore(t)
	r := insertSampleRun(t, s)
	e := insertSampleEvent(t, s, r.ID)

	now := time.Now().UTC()
	for i := 0; i < 15; i++ {
		tc := &engine.ToolCall{
			EventID:   e.ID,
			RunID:     r.ID,
			Tool:      "tool_" + string(rune('a'+i)),
			Timestamp: now,
		}
		_ = s.InsertToolCall(ctx, tc)
	}

	freqs, err := s.TopToolCalls(ctx, now.Add(-time.Minute), now.Add(time.Minute), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(freqs) != 10 {
		t.Errorf("default limit: want 10, got %d", len(freqs))
	}
}

func TestTokenUsageOverTime_InvalidInterval(t *testing.T) {
	s := newMemStore(t)
	_, err := s.TokenUsageOverTime(ctx, time.Now(), time.Now().Add(time.Hour), "bad")
	if err == nil {
		t.Error("expected error for invalid interval")
	}
}

func TestCostOverTime_InvalidInterval(t *testing.T) {
	s := newMemStore(t)
	_, err := s.CostOverTime(ctx, time.Now(), time.Now().Add(time.Hour), "1x")
	if err == nil {
		t.Error("expected error for invalid interval unit")
	}
}

// ─────────────────────────────────────────────────────────────
// parseInterval
// ─────────────────────────────────────────────────────────────

func TestParseInterval(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"1m", time.Minute, false},
		{"30m", 30 * time.Minute, false},
		{"1h", time.Hour, false},
		{"6h", 6 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"", 0, true},
		{"m", 0, true},
		{"0h", 0, true},
		{"1x", 0, true},
		{"abc", 0, true},
	}
	for _, tc := range cases {
		got, err := parseInterval(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseInterval(%q): wantErr=%v, got err=%v", tc.in, tc.wantErr, err)
		}
		if err == nil && got != tc.want {
			t.Errorf("parseInterval(%q): want %v got %v", tc.in, tc.want, got)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Close
// ─────────────────────────────────────────────────────────────

func TestClose(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────
// ID generation (InsertRun with pre-set ID)
// ─────────────────────────────────────────────────────────────

func TestInsertRun_PresetID(t *testing.T) {
	s := newMemStore(t)
	r := sampleRun()
	r.ID = "my-custom-id"
	_ = s.InsertRun(ctx, r)

	got, err := s.GetRun(ctx, "my-custom-id")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "my-custom-id" {
		t.Errorf("preset ID not preserved: %s", got.ID)
	}
}

// ─────────────────────────────────────────────────────────────
// Store implements engine.Store (compile-time verified above)
// ─────────────────────────────────────────────────────────────

func TestStoreInterface(t *testing.T) {
	var _ engine.Store = (*Store)(nil)
}
