// Package drift implements behavioral drift detection for the Spectr engine.
// It computes rolling baselines over a configurable window and identifies
// anomalies using statistical z-score analysis, loop detection, and cost spike
// checks.
package drift

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/Abaddollyon/spectr/pkg/engine"
)

// Default configuration values.
const (
	defaultWindowDays     = 14
	defaultMinSamples     = 5
	defaultRecalcInterval = time.Hour
)

// MetricName identifies a drift-tracking metric.
type MetricName string

const (
	// MetricAvgTokensPerRun is total (in+out) tokens per completed run.
	MetricAvgTokensPerRun MetricName = "avg_tokens_per_run"
	// MetricAvgCostPerRun is total USD cost per completed run.
	MetricAvgCostPerRun MetricName = "avg_cost_per_run"
	// MetricAvgDurationMs is wall-clock duration (ms) per completed run.
	MetricAvgDurationMs MetricName = "avg_duration_ms"
	// MetricToolCallFrequency is the number of tool calls per run.
	MetricToolCallFrequency MetricName = "tool_call_frequency"
	// MetricErrorRate is the fraction of error events within a run.
	MetricErrorRate MetricName = "error_rate"
)

// allMetrics is the full list of tracked metrics (used for iteration).
var allMetrics = []MetricName{
	MetricAvgTokensPerRun,
	MetricAvgCostPerRun,
	MetricAvgDurationMs,
	MetricToolCallFrequency,
	MetricErrorRate,
}

// Baseline holds computed statistics for a single agent + metric pair.
type Baseline struct {
	AgentID     string
	Metric      MetricName
	Mean        float64
	Stddev      float64
	P50         float64
	P95         float64
	P99         float64
	SampleCount int
	ComputedAt  time.Time
	WindowDays  int
}

// baselineKey is the map key for the in-memory baseline cache.
type baselineKey struct {
	agentID string
	metric  MetricName
}

// internalStats holds raw computed statistics before boxing into Baseline.
type internalStats struct {
	mean, stddev float64
	p50, p95, p99 float64
	n             int
}

// Calculator computes and caches rolling baselines for all agents.
// Baselines are stored in-memory and recomputed from the Store on demand.
type Calculator struct {
	mu             sync.RWMutex
	baselines      map[baselineKey]*Baseline
	windowDays     int
	recalcInterval time.Duration
	lastRecalc     time.Time
}

// NewCalculator creates a Calculator with the given parameters.
// Pass 0 to use defaults: 14-day window, 1-hour recalc interval.
func NewCalculator(windowDays int, recalcInterval time.Duration) *Calculator {
	if windowDays <= 0 {
		windowDays = defaultWindowDays
	}
	if recalcInterval <= 0 {
		recalcInterval = defaultRecalcInterval
	}
	return &Calculator{
		baselines:      make(map[baselineKey]*Baseline),
		windowDays:     windowDays,
		recalcInterval: recalcInterval,
	}
}

// NeedsRecalc returns true when the recalculation interval has elapsed since
// the last successful Update call.
func (c *Calculator) NeedsRecalc() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return time.Since(c.lastRecalc) >= c.recalcInterval
}

// Update fetches all completed runs from the rolling window, computes
// per-agent baselines for the five tracked metrics, and replaces the
// in-memory cache atomically.
//
// Agents with fewer than defaultMinSamples runs are skipped — there is not
// enough data for a meaningful baseline yet.
func (c *Calculator) Update(ctx context.Context, store engine.Store) error {
	cutoff := time.Now().Add(-time.Duration(c.windowDays) * 24 * time.Hour)
	runs, err := store.ListRuns(ctx, engine.ListRunsOpts{
		Status: engine.RunStatusCompleted,
		Since:  &cutoff,
		Limit:  10000,
	})
	if err != nil {
		return fmt.Errorf("drift/baseline: list runs: %w", err)
	}

	// Group runs by composite agent key.
	byAgent := make(map[string][]*engine.Run)
	for _, r := range runs {
		if r.AgentID == "" {
			continue
		}
		k := agentKey(r.Framework, r.AgentID)
		byAgent[k] = append(byAgent[k], r)
	}

	newBaselines := make(map[baselineKey]*Baseline)
	now := time.Now()

	for aID, agentRuns := range byAgent {
		if len(agentRuns) < defaultMinSamples {
			continue
		}

		tokenValues := make([]float64, 0, len(agentRuns))
		costValues := make([]float64, 0, len(agentRuns))
		durationValues := make([]float64, 0, len(agentRuns))
		toolCounts := make([]float64, 0, len(agentRuns))
		errorRates := make([]float64, 0, len(agentRuns))

		for _, r := range agentRuns {
			tokenValues = append(tokenValues, float64(r.TotalTokensIn+r.TotalTokensOut))
			costValues = append(costValues, r.TotalCostUSD)

			if r.CompletedAt != nil {
				durationValues = append(durationValues,
					float64(r.CompletedAt.Sub(r.StartedAt).Milliseconds()))
			}

			// Tool call frequency per run.
			tcs, err := store.ListToolCalls(ctx, r.ID)
			if err == nil {
				toolCounts = append(toolCounts, float64(len(tcs)))
			}

			// Error rate: error-type events / total events.
			evts, err := store.ListEvents(ctx, r.ID)
			if err == nil && len(evts) > 0 {
				var errCount int
				for _, e := range evts {
					if e.Type == engine.EventTypeError {
						errCount++
					}
				}
				errorRates = append(errorRates, float64(errCount)/float64(len(evts)))
			}
		}

		metricSamples := map[MetricName][]float64{
			MetricAvgTokensPerRun:   tokenValues,
			MetricAvgCostPerRun:     costValues,
			MetricAvgDurationMs:     durationValues,
			MetricToolCallFrequency: toolCounts,
			MetricErrorRate:         errorRates,
		}

		for metric, values := range metricSamples {
			if len(values) < defaultMinSamples {
				continue
			}
			s := computeStats(values)
			newBaselines[baselineKey{aID, metric}] = &Baseline{
				AgentID:     aID,
				Metric:      metric,
				Mean:        s.mean,
				Stddev:      s.stddev,
				P50:         s.p50,
				P95:         s.p95,
				P99:         s.p99,
				SampleCount: s.n,
				ComputedAt:  now,
				WindowDays:  c.windowDays,
			}
		}
	}

	c.mu.Lock()
	c.baselines = newBaselines
	c.lastRecalc = now
	c.mu.Unlock()

	return nil
}

// Get returns the cached Baseline for the given agent and metric, or nil if
// no baseline has been computed yet.
func (c *Calculator) Get(agentID string, metric MetricName) *Baseline {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.baselines[baselineKey{agentID, metric}]
}

// set stores a baseline directly — used in tests to inject synthetic data.
func (c *Calculator) set(agentID string, metric MetricName, b *Baseline) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.baselines[baselineKey{agentID, metric}] = b
}

// agentKey builds a stable composite key: "framework:agentID".
// If framework is empty only agentID is used.
func agentKey(framework, agentID string) string {
	if framework == "" {
		return agentID
	}
	return framework + ":" + agentID
}

// ─────────────────────────────────────────────────────────────
// Statistics helpers
// ─────────────────────────────────────────────────────────────

// computeStats calculates descriptive statistics for a slice of values.
// It returns sensible zero values for an empty or single-element slice.
func computeStats(values []float64) internalStats {
	n := len(values)
	if n == 0 {
		return internalStats{}
	}

	var sum float64
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(n)

	// Population standard deviation (not sample) — we consider the window the
	// full population we care about.
	var variance float64
	for _, v := range values {
		d := v - mean
		variance += d * d
	}
	variance /= float64(n)
	stddev := math.Sqrt(variance)

	sorted := make([]float64, n)
	copy(sorted, values)
	sort.Float64s(sorted)

	return internalStats{
		mean:   mean,
		stddev: stddev,
		p50:    percentile(sorted, 50),
		p95:    percentile(sorted, 95),
		p99:    percentile(sorted, 99),
		n:      n,
	}
}

// percentile returns the p-th percentile (0–100) of a pre-sorted slice using
// linear interpolation between adjacent ranks.
func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	idx := (p / 100.0) * float64(n-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}
