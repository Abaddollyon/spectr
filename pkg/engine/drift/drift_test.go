package drift_test

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/Abaddollyon/spectr/pkg/engine"
	"github.com/Abaddollyon/spectr/pkg/engine/drift"
)

// ─────────────────────────────────────────────────────────────
// In-memory stub Store
// ─────────────────────────────────────────────────────────────

// stubStore is a minimal in-memory engine.Store used only by tests.
type stubStore struct {
	mu        sync.Mutex
	runs      []*engine.Run
	toolCalls map[string][]*engine.ToolCall // key = runID
	events    map[string][]*engine.Event    // key = runID
	alerts    []*engine.Alert
}

func newStubStore() *stubStore {
	return &stubStore{
		toolCalls: make(map[string][]*engine.ToolCall),
		events:    make(map[string][]*engine.Event),
	}
}

// addRun appends a run to the stub.
func (s *stubStore) addRun(r *engine.Run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = append(s.runs, r)
}

// addToolCalls appends tool calls for a run.
func (s *stubStore) addToolCalls(runID string, tcs ...*engine.ToolCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolCalls[runID] = append(s.toolCalls[runID], tcs...)
}

// addEvents appends events for a run.
func (s *stubStore) addEvents(runID string, evts ...*engine.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[runID] = append(s.events[runID], evts...)
}

// engine.Store implementation ─────────────────────────────────

func (s *stubStore) InsertRun(_ context.Context, r *engine.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = append(s.runs, r)
	return nil
}

func (s *stubStore) UpdateRun(_ context.Context, r *engine.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.runs {
		if existing.ID == r.ID {
			s.runs[i] = r
			return nil
		}
	}
	return fmt.Errorf("run %s not found", r.ID)
}

func (s *stubStore) GetRun(_ context.Context, id string) (*engine.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.runs {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, fmt.Errorf("run %s not found", id)
}

func (s *stubStore) ListRuns(_ context.Context, opts engine.ListRunsOpts) ([]*engine.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*engine.Run
	for _, r := range s.runs {
		if opts.Status != "" && r.Status != opts.Status {
			continue
		}
		if opts.Since != nil && r.StartedAt.Before(*opts.Since) {
			continue
		}
		if opts.Framework != "" && r.Framework != opts.Framework {
			continue
		}
		out = append(out, r)
	}
	if opts.Limit > 0 && len(out) > opts.Limit {
		out = out[:opts.Limit]
	}
	return out, nil
}

func (s *stubStore) InsertEvent(_ context.Context, e *engine.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[e.RunID] = append(s.events[e.RunID], e)
	return nil
}

func (s *stubStore) ListEvents(_ context.Context, runID string) ([]*engine.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.events[runID], nil
}

func (s *stubStore) InsertToolCall(_ context.Context, tc *engine.ToolCall) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolCalls[tc.RunID] = append(s.toolCalls[tc.RunID], tc)
	return nil
}

func (s *stubStore) ListToolCalls(_ context.Context, runID string) ([]*engine.ToolCall, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.toolCalls[runID], nil
}

func (s *stubStore) InsertAlert(_ context.Context, a *engine.Alert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alerts = append(s.alerts, a)
	return nil
}

func (s *stubStore) ListAlerts(_ context.Context, opts engine.ListAlertsOpts) ([]*engine.Alert, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*engine.Alert
	for _, a := range s.alerts {
		if opts.Severity != "" && a.Severity != opts.Severity {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func (s *stubStore) AckAlert(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.alerts {
		if a.ID == id {
			now := time.Now()
			a.AckedAt = &now
			return nil
		}
	}
	return fmt.Errorf("alert %s not found", id)
}

func (s *stubStore) TokenUsageOverTime(_ context.Context, _, _ time.Time, _ string) ([]engine.TimeBucket, error) {
	return nil, nil
}

func (s *stubStore) CostOverTime(_ context.Context, _, _ time.Time, _ string) ([]engine.TimeBucket, error) {
	return nil, nil
}

func (s *stubStore) TopToolCalls(_ context.Context, _, _ time.Time, _ int) ([]engine.ToolFrequency, error) {
	return nil, nil
}

func (s *stubStore) GetCollectorState(_ context.Context, _ string) (*engine.CollectorState, error) {
	return nil, nil
}

func (s *stubStore) SetCollectorState(_ context.Context, _ *engine.CollectorState) error {
	return nil
}

func (s *stubStore) Close() error { return nil }

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────

const testAgentID = "spectr"
const testFramework = "test"

// makeCompletedRun builds a synthetic completed run in the baseline window.
func makeCompletedRun(id string, tokensIn, tokensOut int64, costUSD float64, durationMs int64) *engine.Run {
	now := time.Now()
	completed := now.Add(-time.Duration(durationMs) * time.Millisecond)
	started := completed.Add(-time.Duration(durationMs) * time.Millisecond)
	return &engine.Run{
		ID:             id,
		Framework:      testFramework,
		AgentID:        testAgentID,
		Status:         engine.RunStatusCompleted,
		StartedAt:      started,
		CompletedAt:    &completed,
		TotalTokensIn:  tokensIn,
		TotalTokensOut: tokensOut,
		TotalCostUSD:   costUSD,
	}
}

// populateBaseline seeds the store with n identical synthetic runs so the
// calculator has enough data.
func populateBaseline(store *stubStore, n int, tokensIn, tokensOut int64, costUSD float64, durationMs int64) {
	for i := 0; i < n; i++ {
		r := makeCompletedRun(fmt.Sprintf("base-%d", i), tokensIn, tokensOut, costUSD, durationMs)
		// Spread starts over the last 10 days so all pass the "Since" filter.
		offset := time.Duration(i) * 24 * time.Hour
		r.StartedAt = r.StartedAt.Add(-offset)
		if r.CompletedAt != nil {
			t := (*r.CompletedAt).Add(-offset)
			r.CompletedAt = &t
		}
		store.addRun(r)
	}
}

// ─────────────────────────────────────────────────────────────
// Baseline calculation tests
// ─────────────────────────────────────────────────────────────

// TestBaselineCalculation_Mean verifies that the computed baseline mean
// matches the average of the synthetic data.
func TestBaselineCalculation_Mean(t *testing.T) {
	store := newStubStore()
	// 10 runs: tokens = 1000 in + 500 out = 1500 total, cost = $0.01
	populateBaseline(store, 10, 1000, 500, 0.01, 5000)

	calc := drift.NewCalculator(14, time.Hour)
	if err := calc.Update(context.Background(), store); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	aID := testFramework + ":" + testAgentID
	b := calc.Get(aID, drift.MetricAvgTokensPerRun)
	if b == nil {
		t.Fatal("expected baseline for avg_tokens_per_run, got nil")
	}
	if b.Mean != 1500 {
		t.Errorf("mean tokens = %.2f, want 1500", b.Mean)
	}
	if b.Stddev != 0 {
		t.Errorf("stddev = %.2f, want 0 (all values identical)", b.Stddev)
	}
}

// TestBaselineCalculation_MixedValues verifies mean and stddev with varied data.
func TestBaselineCalculation_MixedValues(t *testing.T) {
	store := newStubStore()

	// Costs: 0.01, 0.02, 0.03, 0.04, 0.05 — mean 0.03
	costs := []float64{0.01, 0.02, 0.03, 0.04, 0.05}
	for i, c := range costs {
		r := makeCompletedRun(fmt.Sprintf("r%d", i), 100, 100, c, 1000)
		offset := time.Duration(i) * 24 * time.Hour
		r.StartedAt = r.StartedAt.Add(-offset)
		if r.CompletedAt != nil {
			t := (*r.CompletedAt).Add(-offset)
			r.CompletedAt = &t
		}
		store.addRun(r)
	}

	calc := drift.NewCalculator(14, time.Hour)
	if err := calc.Update(context.Background(), store); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	aID := testFramework + ":" + testAgentID
	b := calc.Get(aID, drift.MetricAvgCostPerRun)
	if b == nil {
		t.Fatal("expected baseline for avg_cost_per_run, got nil")
	}

	const wantMean = 0.03
	if math.Abs(b.Mean-wantMean) > 1e-9 {
		t.Errorf("mean cost = %.6f, want %.6f", b.Mean, wantMean)
	}
	// Population stddev of {0.01,0.02,0.03,0.04,0.05}: sqrt(0.0002) ≈ 0.01414
	wantStddev := math.Sqrt(0.0002)
	if math.Abs(b.Stddev-wantStddev) > 1e-6 {
		t.Errorf("stddev cost = %.6f, want %.6f", b.Stddev, wantStddev)
	}
}

// TestBaselineCalculation_InsufficientData verifies that fewer than 5 runs
// produce no baseline (not enough data for a meaningful statistic).
func TestBaselineCalculation_InsufficientData(t *testing.T) {
	store := newStubStore()
	populateBaseline(store, 4, 500, 200, 0.05, 1000) // only 4 runs

	calc := drift.NewCalculator(14, time.Hour)
	if err := calc.Update(context.Background(), store); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	aID := testFramework + ":" + testAgentID
	if b := calc.Get(aID, drift.MetricAvgTokensPerRun); b != nil {
		t.Errorf("expected nil baseline with only 4 runs, got %+v", b)
	}
}

// TestBaselineCalculation_ErrorRate verifies the error-rate metric.
func TestBaselineCalculation_ErrorRate(t *testing.T) {
	store := newStubStore()

	// 10 runs, each with 10 events: 1 error each → error_rate = 0.1
	for i := 0; i < 10; i++ {
		r := makeCompletedRun(fmt.Sprintf("er-%d", i), 100, 100, 0.01, 1000)
		offset := time.Duration(i) * 24 * time.Hour
		r.StartedAt = r.StartedAt.Add(-offset)
		if r.CompletedAt != nil {
			t2 := (*r.CompletedAt).Add(-offset)
			r.CompletedAt = &t2
		}
		store.addRun(r)

		// 9 normal events + 1 error event
		for j := 0; j < 9; j++ {
			store.addEvents(r.ID, &engine.Event{
				ID:    fmt.Sprintf("evt-%d-%d", i, j),
				RunID: r.ID,
				Type:  engine.EventTypeMessage,
			})
		}
		store.addEvents(r.ID, &engine.Event{
			ID:    fmt.Sprintf("err-%d", i),
			RunID: r.ID,
			Type:  engine.EventTypeError,
		})
	}

	calc := drift.NewCalculator(14, time.Hour)
	if err := calc.Update(context.Background(), store); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	aID := testFramework + ":" + testAgentID
	b := calc.Get(aID, drift.MetricErrorRate)
	if b == nil {
		t.Fatal("expected baseline for error_rate, got nil")
	}
	const wantMean = 0.1
	if math.Abs(b.Mean-wantMean) > 1e-9 {
		t.Errorf("error_rate mean = %.4f, want %.4f", b.Mean, wantMean)
	}
}

// TestBaselineCalculation_ToolCallFrequency verifies tool call frequency.
func TestBaselineCalculation_ToolCallFrequency(t *testing.T) {
	store := newStubStore()

	// 10 runs, each with exactly 7 tool calls.
	for i := 0; i < 10; i++ {
		r := makeCompletedRun(fmt.Sprintf("tcf-%d", i), 100, 100, 0.01, 1000)
		offset := time.Duration(i) * 24 * time.Hour
		r.StartedAt = r.StartedAt.Add(-offset)
		if r.CompletedAt != nil {
			t2 := (*r.CompletedAt).Add(-offset)
			r.CompletedAt = &t2
		}
		store.addRun(r)

		for j := 0; j < 7; j++ {
			store.addToolCalls(r.ID, &engine.ToolCall{
				ID:    fmt.Sprintf("tc-%d-%d", i, j),
				RunID: r.ID,
				Tool:  "exec",
			})
		}
	}

	calc := drift.NewCalculator(14, time.Hour)
	if err := calc.Update(context.Background(), store); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	aID := testFramework + ":" + testAgentID
	b := calc.Get(aID, drift.MetricToolCallFrequency)
	if b == nil {
		t.Fatal("expected baseline for tool_call_frequency, got nil")
	}
	if b.Mean != 7.0 {
		t.Errorf("tool_call_frequency mean = %.2f, want 7.0", b.Mean)
	}
}

// ─────────────────────────────────────────────────────────────
// Z-score alerting tests
// ─────────────────────────────────────────────────────────────

// TestZScore_Warning verifies that a value at 2.7σ above the baseline mean
// triggers a SeverityWarning drift alert (threshold is 2.5σ).
func TestZScore_Warning(t *testing.T) {
	store := newStubStore()

	const (
		mean   = 1000.0
		stddev = 100.0
	)

	// Build baseline with 5 symmetric pairs; spread across past days so they
	// all fall within the 14-day Update window.
	for i := 0; i < 5; i++ {
		offset := time.Duration(i+1) * 24 * time.Hour // 1–5 days ago (outside 1h analyze window)
		rLow := makeCompletedRun(fmt.Sprintf("low-%d", i), int64(mean-stddev), 0, 0.01, 1000)
		rLow.StartedAt = rLow.StartedAt.Add(-offset)
		if rLow.CompletedAt != nil {
			t2 := (*rLow.CompletedAt).Add(-offset)
			rLow.CompletedAt = &t2
		}
		store.addRun(rLow)

		rHigh := makeCompletedRun(fmt.Sprintf("high-%d", i), int64(mean+stddev), 0, 0.01, 1000)
		rHigh.StartedAt = rHigh.StartedAt.Add(-offset)
		if rHigh.CompletedAt != nil {
			t2 := (*rHigh.CompletedAt).Add(-offset)
			rHigh.CompletedAt = &t2
		}
		store.addRun(rHigh)
	}

	// Compute baseline from the 10 historical runs.
	calc := drift.NewCalculator(14, time.Hour)
	if err := calc.Update(context.Background(), store); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	aID := testFramework + ":" + testAgentID
	b := calc.Get(aID, drift.MetricAvgTokensPerRun)
	if b == nil {
		t.Fatal("no baseline computed")
	}
	if math.Abs(b.Mean-mean) > 0.1 {
		t.Fatalf("mean = %.2f want %.2f", b.Mean, mean)
	}

	// Add the anomaly AFTER computing the baseline so it isn't baked in.
	// Use 2.7σ (above the 2.5σ threshold, but below 4σ critical).
	warnValue := b.Mean + 2.7*b.Stddev
	anomalyRun := makeCompletedRun("anomaly-warn", int64(warnValue), 0, 0.01, 1000)
	store.addRun(anomalyRun) // recent: StartedAt ≈ now-2s, within 1h window

	// Use the SAME calculator — NeedsRecalc is false (just updated), so
	// Analyze will not re-compute the baseline from the distorted store.
	det := drift.NewWithCalculator(calc)
	alerts, err := det.Analyze(context.Background(), store)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	var found bool
	for _, a := range alerts {
		if a.RunID != nil && *a.RunID == anomalyRun.ID &&
			a.Type == engine.AlertTypeDrift &&
			a.Severity == engine.SeverityWarning {
			found = true
			if a.ZScore < 2.5 {
				t.Errorf("z-score = %.2f, expected >= 2.5", a.ZScore)
			}
		}
	}
	if !found {
		t.Errorf("expected SeverityWarning drift alert for run %s, got alerts: %v",
			anomalyRun.ID, summarizeAlerts(alerts))
	}
}

// TestZScore_Critical verifies that a value at 4.5σ above baseline triggers
// a SeverityCritical drift alert (critical threshold is 4σ).
func TestZScore_Critical(t *testing.T) {
	store := newStubStore()

	const (
		mean   = 1000.0
		stddev = 100.0
	)

	// Build symmetric baseline spread across past days (outside analyze window).
	for i := 0; i < 5; i++ {
		offset := time.Duration(i+1) * 24 * time.Hour
		rLow := makeCompletedRun(fmt.Sprintf("clow-%d", i), int64(mean-stddev), 0, 0.01, 1000)
		rLow.StartedAt = rLow.StartedAt.Add(-offset)
		if rLow.CompletedAt != nil {
			t2 := (*rLow.CompletedAt).Add(-offset)
			rLow.CompletedAt = &t2
		}
		store.addRun(rLow)

		rHigh := makeCompletedRun(fmt.Sprintf("chigh-%d", i), int64(mean+stddev), 0, 0.01, 1000)
		rHigh.StartedAt = rHigh.StartedAt.Add(-offset)
		if rHigh.CompletedAt != nil {
			t2 := (*rHigh.CompletedAt).Add(-offset)
			rHigh.CompletedAt = &t2
		}
		store.addRun(rHigh)
	}

	calc := drift.NewCalculator(14, time.Hour)
	if err := calc.Update(context.Background(), store); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	b := calc.Get(testFramework+":"+testAgentID, drift.MetricAvgTokensPerRun)
	if b == nil {
		t.Fatal("no baseline")
	}

	// Add anomaly at 4.5σ AFTER baseline is computed.
	critValue := b.Mean + 4.5*b.Stddev
	anomaly := makeCompletedRun("anomaly-crit", int64(critValue), 0, 0.01, 1000)
	store.addRun(anomaly) // recent: within 1h window

	// Use the SAME calc — NeedsRecalc is false.
	det := drift.NewWithCalculator(calc)
	alerts, err := det.Analyze(context.Background(), store)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	var found bool
	for _, a := range alerts {
		if a.RunID != nil && *a.RunID == anomaly.ID &&
			a.Type == engine.AlertTypeDrift &&
			a.Severity == engine.SeverityCritical {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SeverityCritical drift alert, got: %v", summarizeAlerts(alerts))
	}
}

// TestZScore_NoAlert verifies that a value below 2.5σ produces no alert.
func TestZScore_NoAlert(t *testing.T) {
	store := newStubStore()

	const (
		mean   = 1000.0
		stddev = 100.0
	)

	for i := 0; i < 5; i++ {
		offset := time.Duration(i+1) * 24 * time.Hour
		rLow := makeCompletedRun(fmt.Sprintf("nlow-%d", i), int64(mean-stddev), 0, 0.01, 1000)
		rLow.StartedAt = rLow.StartedAt.Add(-offset)
		if rLow.CompletedAt != nil {
			t2 := (*rLow.CompletedAt).Add(-offset)
			rLow.CompletedAt = &t2
		}
		store.addRun(rLow)

		rHigh := makeCompletedRun(fmt.Sprintf("nhigh-%d", i), int64(mean+stddev), 0, 0.01, 1000)
		rHigh.StartedAt = rHigh.StartedAt.Add(-offset)
		if rHigh.CompletedAt != nil {
			t2 := (*rHigh.CompletedAt).Add(-offset)
			rHigh.CompletedAt = &t2
		}
		store.addRun(rHigh)
	}

	calc := drift.NewCalculator(14, time.Hour)
	if err := calc.Update(context.Background(), store); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	b := calc.Get(testFramework+":"+testAgentID, drift.MetricAvgTokensPerRun)
	if b == nil {
		t.Fatal("no baseline")
	}

	// Run at only 1σ above mean — no alert expected.
	normalValue := b.Mean + 1.0*b.Stddev
	normal := makeCompletedRun("normal-run", int64(normalValue), 0, 0.01, 1000)
	store.addRun(normal)

	// Use the same calc — NeedsRecalc is false, baseline stays clean.
	det := drift.NewWithCalculator(calc)
	alerts, err := det.Analyze(context.Background(), store)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	for _, a := range alerts {
		if a.RunID != nil && *a.RunID == normal.ID && a.Type == engine.AlertTypeDrift {
			t.Errorf("unexpected drift alert for normal run: %+v", a)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Loop detection tests
// ─────────────────────────────────────────────────────────────

// TestLoopDetection_Triggered verifies that 5+ identical (tool, args_hash)
// calls within the loop window produce an AlertTypeLoopDetected alert.
func TestLoopDetection_Triggered(t *testing.T) {
	store := newStubStore()

	// Running run so Analyze picks it up under the "active" branch.
	runID := "loop-run"
	r := &engine.Run{
		ID:        runID,
		Framework: testFramework,
		AgentID:   testAgentID,
		Status:    engine.RunStatusRunning,
		StartedAt: time.Now().Add(-10 * time.Minute),
	}
	store.addRun(r)

	// Inject 5 identical tool calls within the last 5 minutes.
	now := time.Now()
	for i := 0; i < 5; i++ {
		store.addToolCalls(runID, &engine.ToolCall{
			ID:        fmt.Sprintf("loop-tc-%d", i),
			RunID:     runID,
			Tool:      "exec",
			ArgsHash:  "abc123",
			Timestamp: now.Add(-time.Duration(i) * time.Minute),
		})
	}

	det := drift.New()
	alerts, err := det.Analyze(context.Background(), store)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	var found bool
	for _, a := range alerts {
		if a.Type == engine.AlertTypeLoopDetected && a.Severity == engine.SeverityCritical {
			found = true
			if a.Actual < 5 {
				t.Errorf("loop alert actual = %.0f, want >= 5", a.Actual)
			}
		}
	}
	if !found {
		t.Errorf("expected loop alert, got: %v", summarizeAlerts(alerts))
	}
}

// TestLoopDetection_BelowThreshold verifies that 4 identical calls (one
// below the threshold) do not trigger an alert.
func TestLoopDetection_BelowThreshold(t *testing.T) {
	store := newStubStore()

	runID := "no-loop-run"
	r := &engine.Run{
		ID:        runID,
		Framework: testFramework,
		AgentID:   testAgentID,
		Status:    engine.RunStatusRunning,
		StartedAt: time.Now().Add(-10 * time.Minute),
	}
	store.addRun(r)

	now := time.Now()
	for i := 0; i < 4; i++ {
		store.addToolCalls(runID, &engine.ToolCall{
			ID:        fmt.Sprintf("safe-tc-%d", i),
			RunID:     runID,
			Tool:      "read",
			ArgsHash:  "def456",
			Timestamp: now.Add(-time.Duration(i) * time.Minute),
		})
	}

	det := drift.New()
	alerts, err := det.Analyze(context.Background(), store)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	for _, a := range alerts {
		if a.Type == engine.AlertTypeLoopDetected {
			t.Errorf("unexpected loop alert: %+v", a)
		}
	}
}

// TestLoopDetection_DifferentArgsHashNoAlert verifies that many calls to the
// same tool with different args hashes do not trigger a loop alert.
func TestLoopDetection_DifferentArgsHashNoAlert(t *testing.T) {
	store := newStubStore()

	runID := "varied-args-run"
	r := &engine.Run{
		ID:        runID,
		Framework: testFramework,
		AgentID:   testAgentID,
		Status:    engine.RunStatusRunning,
		StartedAt: time.Now().Add(-10 * time.Minute),
	}
	store.addRun(r)

	now := time.Now()
	// 10 calls, each with a unique args hash.
	for i := 0; i < 10; i++ {
		store.addToolCalls(runID, &engine.ToolCall{
			ID:        fmt.Sprintf("varied-tc-%d", i),
			RunID:     runID,
			Tool:      "web_fetch",
			ArgsHash:  fmt.Sprintf("hash-%d", i),
			Timestamp: now.Add(-time.Duration(i) * 30 * time.Second),
		})
	}

	det := drift.New()
	alerts, err := det.Analyze(context.Background(), store)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}
	for _, a := range alerts {
		if a.Type == engine.AlertTypeLoopDetected {
			t.Errorf("unexpected loop alert for varied-args run: %+v", a)
		}
	}
}

// TestLoopDetection_OutsideWindow verifies that calls older than 5 minutes
// are excluded from loop detection.
func TestLoopDetection_OutsideWindow(t *testing.T) {
	store := newStubStore()

	runID := "old-loop-run"
	r := &engine.Run{
		ID:        runID,
		Framework: testFramework,
		AgentID:   testAgentID,
		Status:    engine.RunStatusRunning,
		StartedAt: time.Now().Add(-30 * time.Minute),
	}
	store.addRun(r)

	// 5 identical calls, but all > 5 minutes ago.
	base := time.Now().Add(-10 * time.Minute)
	for i := 0; i < 5; i++ {
		store.addToolCalls(runID, &engine.ToolCall{
			ID:        fmt.Sprintf("old-tc-%d", i),
			RunID:     runID,
			Tool:      "exec",
			ArgsHash:  "stale",
			Timestamp: base.Add(-time.Duration(i) * 30 * time.Second),
		})
	}

	det := drift.New()
	alerts, err := det.Analyze(context.Background(), store)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}
	for _, a := range alerts {
		if a.Type == engine.AlertTypeLoopDetected {
			t.Errorf("unexpected loop alert for out-of-window calls: %+v", a)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// Cost spike tests
// ─────────────────────────────────────────────────────────────

// TestCostSpike_Triggered verifies that a run costing > 3x baseline mean
// triggers an AlertTypeCostSpike at SeverityCritical.
func TestCostSpike_Triggered(t *testing.T) {
	store := newStubStore()

	// 10 baseline runs all costing $0.10; spread >1 day back so they're
	// outside the Analyze recent-window but inside the 14-day Update window.
	const baselineCost = 0.10
	for i := 0; i < 10; i++ {
		r := makeCompletedRun(fmt.Sprintf("cs-base-%d", i), 100, 50, baselineCost, 1000)
		offset := time.Duration(i+1) * 24 * time.Hour
		r.StartedAt = r.StartedAt.Add(-offset)
		if r.CompletedAt != nil {
			t2 := (*r.CompletedAt).Add(-offset)
			r.CompletedAt = &t2
		}
		store.addRun(r)
	}

	// Compute baseline from those 10 historical runs.
	calc := drift.NewCalculator(14, time.Hour)
	if err := calc.Update(context.Background(), store); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	b := calc.Get(testFramework+":"+testAgentID, drift.MetricAvgCostPerRun)
	if b == nil {
		t.Fatal("no baseline for cost")
	}

	// Add the spike run AFTER computing the baseline — 3.5x mean.
	spikeRun := makeCompletedRun("spike-run", 100, 50, b.Mean*3.5, 1000)
	store.addRun(spikeRun) // recent: within 1h analyze window

	// Use the SAME calc (NeedsRecalc=false) so the spike isn't baked in.
	det := drift.NewWithCalculator(calc)
	alerts, err := det.Analyze(context.Background(), store)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	var found bool
	for _, a := range alerts {
		if a.RunID != nil && *a.RunID == spikeRun.ID &&
			a.Type == engine.AlertTypeCostSpike &&
			a.Severity == engine.SeverityCritical {
			found = true
			if a.Actual <= a.Expected*costSpikeMultiplier {
				t.Errorf("spike alert: actual $%.4f should be > %.1fx expected $%.4f",
					a.Actual, costSpikeMultiplier, a.Expected)
			}
		}
	}
	if !found {
		t.Errorf("expected cost spike alert, got: %v", summarizeAlerts(alerts))
	}
}

const costSpikeMultiplier = 3.0

// TestCostSpike_NotTriggered verifies that a run at 2x baseline mean does not
// trigger a cost spike alert (threshold is 3x).
func TestCostSpike_NotTriggered(t *testing.T) {
	store := newStubStore()

	for i := 0; i < 10; i++ {
		r := makeCompletedRun(fmt.Sprintf("csnot-base-%d", i), 100, 50, 0.10, 1000)
		offset := time.Duration(i+1) * 24 * time.Hour
		r.StartedAt = r.StartedAt.Add(-offset)
		if r.CompletedAt != nil {
			t2 := (*r.CompletedAt).Add(-offset)
			r.CompletedAt = &t2
		}
		store.addRun(r)
	}

	calc := drift.NewCalculator(14, time.Hour)
	if err := calc.Update(context.Background(), store); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	b := calc.Get(testFramework+":"+testAgentID, drift.MetricAvgCostPerRun)
	if b == nil {
		t.Fatal("no baseline for cost")
	}

	// 2x mean — below the 3x threshold.
	normalRun := makeCompletedRun("normal-cost-run", 100, 50, b.Mean*2.0, 1000)
	store.addRun(normalRun)

	det := drift.NewWithCalculator(calc)
	alerts, err := det.Analyze(context.Background(), store)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	for _, a := range alerts {
		if a.RunID != nil && *a.RunID == normalRun.ID && a.Type == engine.AlertTypeCostSpike {
			t.Errorf("unexpected cost spike alert at 2x mean: %+v", a)
		}
	}
}

// TestCostSpike_NoBaselineNoAlert verifies that without a baseline (< 5 runs)
// no cost spike alert is produced.
func TestCostSpike_NoBaselineNoAlert(t *testing.T) {
	store := newStubStore()

	// Only 2 baseline runs — not enough for a valid baseline.
	for i := 0; i < 2; i++ {
		r := makeCompletedRun(fmt.Sprintf("nb-base-%d", i), 100, 50, 0.10, 1000)
		offset := time.Duration(i+1) * 24 * time.Hour
		r.StartedAt = r.StartedAt.Add(-offset)
		if r.CompletedAt != nil {
			t2 := (*r.CompletedAt).Add(-offset)
			r.CompletedAt = &t2
		}
		store.addRun(r)
	}

	calc := drift.NewCalculator(14, time.Hour)
	if err := calc.Update(context.Background(), store); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	// Absurdly expensive run — but no baseline to compare against.
	expensiveRun := makeCompletedRun("no-baseline-run", 100, 50, 9999.0, 1000)
	store.addRun(expensiveRun)

	det := drift.NewWithCalculator(calc)
	alerts, err := det.Analyze(context.Background(), store)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	for _, a := range alerts {
		if a.RunID != nil && *a.RunID == expensiveRun.ID && a.Type == engine.AlertTypeCostSpike {
			t.Errorf("unexpected cost spike without baseline: %+v", a)
		}
	}
}

// ─────────────────────────────────────────────────────────────
// UpdateBaseline integration test
// ─────────────────────────────────────────────────────────────

// TestUpdateBaseline_Interface verifies that Detector.UpdateBaseline satisfies
// the engine.DriftDetector interface contract.
func TestUpdateBaseline_Interface(t *testing.T) {
	store := newStubStore()
	populateBaseline(store, 10, 500, 200, 0.05, 2000)

	var det engine.DriftDetector = drift.New()
	if err := det.UpdateBaseline(context.Background(), store); err != nil {
		t.Fatalf("UpdateBaseline() error: %v", err)
	}
	// Calling again should not error (idempotent).
	if err := det.UpdateBaseline(context.Background(), store); err != nil {
		t.Fatalf("second UpdateBaseline() error: %v", err)
	}
}

// TestAnalyze_Interface verifies the full interface contract end-to-end.
func TestAnalyze_Interface(t *testing.T) {
	store := newStubStore()
	populateBaseline(store, 10, 500, 200, 0.05, 2000)

	var det engine.DriftDetector = drift.New()
	if err := det.UpdateBaseline(context.Background(), store); err != nil {
		t.Fatalf("UpdateBaseline() error: %v", err)
	}
	alerts, err := det.Analyze(context.Background(), store)
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}
	// No anomalies in a uniform baseline — expect zero alerts.
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts for uniform baseline, got %d: %v", len(alerts), summarizeAlerts(alerts))
	}
}

// ─────────────────────────────────────────────────────────────
// NeedsRecalc test
// ─────────────────────────────────────────────────────────────

func TestNeedsRecalc(t *testing.T) {
	// Very short recalc interval — should need recalc immediately after creation.
	calc := drift.NewCalculator(14, time.Millisecond)
	if !calc.NeedsRecalc() {
		t.Error("expected NeedsRecalc() = true for fresh calculator")
	}

	store := newStubStore()
	populateBaseline(store, 10, 100, 50, 0.01, 500)
	if err := calc.Update(context.Background(), store); err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	// After update, should NOT need recalc (we just updated).
	if calc.NeedsRecalc() {
		t.Error("expected NeedsRecalc() = false immediately after Update()")
	}

	// After the interval elapses, should need recalc again.
	time.Sleep(2 * time.Millisecond)
	if !calc.NeedsRecalc() {
		t.Error("expected NeedsRecalc() = true after interval elapsed")
	}
}

// ─────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────

func summarizeAlerts(alerts []*engine.Alert) []string {
	var out []string
	for _, a := range alerts {
		runID := "<nil>"
		if a.RunID != nil {
			runID = *a.RunID
		}
		out = append(out, fmt.Sprintf("{type=%s sev=%s run=%s metric=%s z=%.2f}",
			a.Type, a.Severity, runID, a.Metric, a.ZScore))
	}
	return out
}
