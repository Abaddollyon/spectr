package engine

import (
	"context"
	"time"
)

// Store is the core storage interface. SQLite implements this.
// All components depend on this interface, never on a concrete implementation.
type Store interface {
	// Runs
	InsertRun(ctx context.Context, run *Run) error
	UpdateRun(ctx context.Context, run *Run) error
	GetRun(ctx context.Context, id string) (*Run, error)
	ListRuns(ctx context.Context, opts ListRunsOpts) ([]*Run, error)

	// Events
	InsertEvent(ctx context.Context, event *Event) error
	ListEvents(ctx context.Context, runID string) ([]*Event, error)

	// Tool calls
	InsertToolCall(ctx context.Context, tc *ToolCall) error
	ListToolCalls(ctx context.Context, runID string) ([]*ToolCall, error)

	// Alerts
	InsertAlert(ctx context.Context, alert *Alert) error
	ListAlerts(ctx context.Context, opts ListAlertsOpts) ([]*Alert, error)
	AckAlert(ctx context.Context, id string) error

	// Aggregations
	TokenUsageOverTime(ctx context.Context, from, to time.Time, interval string) ([]TimeBucket, error)
	CostOverTime(ctx context.Context, from, to time.Time, interval string) ([]TimeBucket, error)
	TopToolCalls(ctx context.Context, from, to time.Time, limit int) ([]ToolFrequency, error)

	// Collector state (for restart recovery)
	GetCollectorState(ctx context.Context, collectorID string) (*CollectorState, error)
	SetCollectorState(ctx context.Context, state *CollectorState) error

	// Lifecycle
	Close() error
}

// Collector watches an agent framework and feeds events into the Store.
type Collector interface {
	// Name returns the framework identifier (e.g., "openclaw", "claudecode").
	Name() string

	// Discover auto-detects session file paths for this framework.
	Discover() ([]string, error)

	// Start begins watching and ingesting. Blocks until ctx is cancelled.
	Start(ctx context.Context, store Store) error
}

// DriftDetector analyzes events against baselines and produces alerts.
type DriftDetector interface {
	// Analyze checks recent activity against baseline and returns any alerts.
	Analyze(ctx context.Context, store Store) ([]*Alert, error)

	// UpdateBaseline recalculates the rolling baseline.
	UpdateBaseline(ctx context.Context, store Store) error
}

// ListRunsOpts controls run listing queries.
type ListRunsOpts struct {
	Framework string
	Status    RunStatus
	Since     *time.Time
	Limit     int
	Offset    int
}

// ListAlertsOpts controls alert listing queries.
type ListAlertsOpts struct {
	Severity Severity
	AckedOnly bool
	Since    *time.Time
	Limit    int
}

// TimeBucket is a single time-series data point.
type TimeBucket struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// ToolFrequency tracks how often a tool is called.
type ToolFrequency struct {
	Tool  string `json:"tool"`
	Count int64  `json:"count"`
}

// CollectorState tracks file offsets for restart recovery.
type CollectorState struct {
	CollectorID string `json:"collector_id"`
	FilePath    string `json:"file_path"`
	ByteOffset  int64  `json:"byte_offset"`
	LastEventID string `json:"last_event_id"`
}
