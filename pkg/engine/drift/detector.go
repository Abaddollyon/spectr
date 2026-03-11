package drift

import (
	"context"
	"fmt"
	"time"

	"github.com/Abaddollyon/spectr/pkg/engine"
	"github.com/google/uuid"
)

// Detection thresholds.
const (
	// warningSigma is the z-score that triggers a SeverityWarning alert.
	warningSigma = 2.5
	// criticalSigma is the z-score that triggers a SeverityCritical alert.
	criticalSigma = 4.0

	// loopWindow is the look-back duration for loop detection.
	loopWindow = 5 * time.Minute
	// loopThreshold is the minimum number of identical (tool, args_hash) calls
	// within loopWindow that triggers a loop alert.
	loopThreshold = 5

	// costSpikeMultiplier: a single run costing more than this multiple of
	// the baseline mean triggers an AlertTypeCostSpike.
	costSpikeMultiplier = 3.0
)

// Detector implements engine.DriftDetector using z-score analysis, loop
// detection, and cost spike checks.
type Detector struct {
	calc *Calculator
}

// Compile-time check that *Detector satisfies engine.DriftDetector.
var _ engine.DriftDetector = (*Detector)(nil)

// New creates a Detector with default baseline settings (14-day window,
// 1-hour recalculation interval).
func New() *Detector {
	return &Detector{calc: NewCalculator(0, 0)}
}

// NewWithCalculator creates a Detector backed by the supplied Calculator,
// allowing the caller to configure window size and recalc interval.
func NewWithCalculator(calc *Calculator) *Detector {
	return &Detector{calc: calc}
}

// UpdateBaseline recalculates the rolling baseline from the store's
// historical run data. It satisfies engine.DriftDetector.UpdateBaseline.
func (d *Detector) UpdateBaseline(ctx context.Context, store engine.Store) error {
	return d.calc.Update(ctx, store)
}

// Analyze inspects recent activity against the current baseline and returns
// all generated alerts. Each alert is also persisted to the store via
// engine.Store.InsertAlert before being returned.
//
// The method performs three checks:
//  1. Per-metric z-score on completed runs since the last recalc interval.
//  2. Cost spike on those same completed runs.
//  3. Tool-call loop detection on active and recently completed runs.
//
// If the baseline is stale (or has never been computed) it is refreshed first.
func (d *Detector) Analyze(ctx context.Context, store engine.Store) ([]*engine.Alert, error) {
	// Refresh baseline if needed.
	if d.calc.NeedsRecalc() {
		if err := d.calc.Update(ctx, store); err != nil {
			// Non-fatal: log but continue — we may still have a cached baseline.
			_ = fmt.Errorf("drift/analyze: update baseline: %w", err)
		}
	}

	// Examine runs that completed since the last recalc window started.
	analyzeSince := time.Now().Add(-d.calc.recalcInterval)
	recentRuns, err := store.ListRuns(ctx, engine.ListRunsOpts{
		Status: engine.RunStatusCompleted,
		Since:  &analyzeSince,
		Limit:  500,
	})
	if err != nil {
		return nil, fmt.Errorf("drift/analyze: list recent runs: %w", err)
	}

	var alerts []*engine.Alert

	for _, run := range recentRuns {
		aID := agentKey(run.Framework, run.AgentID)

		// 1. Z-score anomaly detection.
		alerts = append(alerts, d.checkZScores(run, aID)...)

		// 2. Cost spike.
		if a := d.checkCostSpike(run, aID); a != nil {
			alerts = append(alerts, a)
		}
	}

	// 3. Loop detection: check currently running and recently completed runs.
	loopSince := time.Now().Add(-loopWindow)
	activeSince := time.Now().Add(-time.Hour)

	runningRuns, err := store.ListRuns(ctx, engine.ListRunsOpts{
		Status: engine.RunStatusRunning,
		Since:  &activeSince,
		Limit:  100,
	})
	if err == nil {
		for _, run := range runningRuns {
			if a := d.checkLoops(ctx, store, run.ID, loopSince); a != nil {
				alerts = append(alerts, a)
			}
		}
	}

	for _, run := range recentRuns {
		if a := d.checkLoops(ctx, store, run.ID, loopSince); a != nil {
			alerts = append(alerts, a)
		}
	}

	// Persist all alerts.
	for _, a := range alerts {
		_ = store.InsertAlert(ctx, a)
	}

	return alerts, nil
}

// ─────────────────────────────────────────────────────────────
// Z-score checks
// ─────────────────────────────────────────────────────────────

// checkZScores evaluates all applicable metrics for run against the cached
// baseline and returns any triggered alerts.
func (d *Detector) checkZScores(run *engine.Run, agentID string) []*engine.Alert {
	type check struct {
		metric MetricName
		value  float64
		valid  bool
	}

	checks := []check{
		{MetricAvgTokensPerRun, float64(run.TotalTokensIn + run.TotalTokensOut), true},
		{MetricAvgCostPerRun, run.TotalCostUSD, true},
	}
	if run.CompletedAt != nil {
		checks = append(checks, check{
			MetricAvgDurationMs,
			float64(run.CompletedAt.Sub(run.StartedAt).Milliseconds()),
			true,
		})
	}

	var alerts []*engine.Alert
	for _, c := range checks {
		if !c.valid {
			continue
		}
		b := d.calc.Get(agentID, c.metric)
		if b == nil || b.SampleCount < defaultMinSamples {
			continue
		}
		if a := zScoreAlert(run, c.metric, c.value, b); a != nil {
			alerts = append(alerts, a)
		}
	}
	return alerts
}

// zScoreAlert computes the z-score for value against b and returns an Alert
// when the value exceeds the warning threshold; returns nil otherwise.
func zScoreAlert(run *engine.Run, metric MetricName, value float64, b *Baseline) *engine.Alert {
	if b.Stddev == 0 {
		return nil
	}
	sigma := (value - b.Mean) / b.Stddev
	if sigma < warningSigma {
		return nil
	}

	severity := engine.SeverityWarning
	if sigma >= criticalSigma {
		severity = engine.SeverityCritical
	}

	msg := fmt.Sprintf(
		"%s is %.2fσ above baseline (actual=%.4g mean=%.4g stddev=%.4g)",
		metric, sigma, value, b.Mean, b.Stddev,
	)

	a := newAlert(engine.AlertTypeDrift, severity, string(metric), msg)
	a.RunID = &run.ID
	a.Expected = b.Mean
	a.Actual = value
	a.ZScore = sigma
	return a
}

// ─────────────────────────────────────────────────────────────
// Cost spike
// ─────────────────────────────────────────────────────────────

// checkCostSpike returns an alert when run's cost exceeds costSpikeMultiplier
// times the baseline mean.
func (d *Detector) checkCostSpike(run *engine.Run, agentID string) *engine.Alert {
	b := d.calc.Get(agentID, MetricAvgCostPerRun)
	if b == nil || b.Mean == 0 || b.SampleCount < defaultMinSamples {
		return nil
	}

	threshold := b.Mean * costSpikeMultiplier
	if run.TotalCostUSD <= threshold {
		return nil
	}

	var sigma float64
	if b.Stddev > 0 {
		sigma = (run.TotalCostUSD - b.Mean) / b.Stddev
	}

	msg := fmt.Sprintf(
		"cost spike: $%.4f is %.1fx baseline mean ($%.4f)",
		run.TotalCostUSD, run.TotalCostUSD/b.Mean, b.Mean,
	)

	a := newAlert(engine.AlertTypeCostSpike, engine.SeverityCritical,
		string(MetricAvgCostPerRun), msg)
	a.RunID = &run.ID
	a.Expected = b.Mean
	a.Actual = run.TotalCostUSD
	a.ZScore = sigma
	return a
}

// ─────────────────────────────────────────────────────────────
// Loop detection
// ─────────────────────────────────────────────────────────────

// checkLoops fetches tool calls for runID, counts identical (tool, args_hash)
// pairs within [since, now], and returns an alert when loopThreshold is
// reached. Returns nil if no loop is detected.
func (d *Detector) checkLoops(
	ctx context.Context,
	store engine.Store,
	runID string,
	since time.Time,
) *engine.Alert {
	tcs, err := store.ListToolCalls(ctx, runID)
	if err != nil {
		return nil
	}

	type loopKey struct{ tool, argsHash string }
	counts := make(map[loopKey]int)
	latest := make(map[loopKey]*engine.ToolCall)

	for _, tc := range tcs {
		if tc.Timestamp.Before(since) {
			continue
		}
		k := loopKey{tc.Tool, tc.ArgsHash}
		counts[k]++
		latest[k] = tc
	}

	for k, count := range counts {
		if count >= loopThreshold {
			tc := latest[k]
			msg := fmt.Sprintf(
				"loop detected: tool %q called %d times with identical args (hash=%s) in last %v",
				tc.Tool, count, tc.ArgsHash, loopWindow,
			)
			a := newAlert(engine.AlertTypeLoopDetected, engine.SeverityCritical,
				"tool_loop", msg)
			a.RunID = &runID
			a.Actual = float64(count)
			a.Expected = float64(loopThreshold - 1)
			return a // one alert per run per check cycle
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────
// Alert construction helper
// ─────────────────────────────────────────────────────────────

// newAlert constructs an Alert with a fresh UUID and current timestamp.
// RunID and metric values are set by the caller.
func newAlert(
	alertType engine.AlertType,
	severity engine.Severity,
	metric string,
	msg string,
) *engine.Alert {
	return &engine.Alert{
		ID:        uuid.New().String(),
		Type:      alertType,
		Severity:  severity,
		Metric:    metric,
		Message:   msg,
		CreatedAt: time.Now(),
	}
}
