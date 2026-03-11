// Package engine defines the core types for Spectr agent observability.
package engine

import "time"

// Run represents a single agent execution (top-level or sub-agent).
type Run struct {
	ID            string     `json:"id"`
	ParentRunID   *string    `json:"parent_run_id,omitempty"`
	Framework     string     `json:"framework"` // openclaw, claudecode, codexcli
	AgentID       string     `json:"agent_id"`
	SessionKey    string     `json:"session_key"`
	Status        RunStatus  `json:"status"`
	StartedAt     time.Time  `json:"started_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	TotalTokensIn int64      `json:"total_tokens_in"`
	TotalTokensOut int64     `json:"total_tokens_out"`
	TotalCostUSD  float64    `json:"total_cost_usd"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

type RunStatus string

const (
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
	RunStatusTimeout   RunStatus = "timeout"
)

// Event represents a single action within a run.
type Event struct {
	ID            string    `json:"id"`
	RunID         string    `json:"run_id"`
	ParentEventID *string   `json:"parent_event_id,omitempty"`
	Type          EventType `json:"type"`
	Timestamp     time.Time `json:"timestamp"`
	DurationMs    *int64    `json:"duration_ms,omitempty"`
	TokensIn      int64     `json:"tokens_in"`
	TokensOut     int64     `json:"tokens_out"`
	CostUSD       float64   `json:"cost_usd"`
	Model         string    `json:"model,omitempty"`
	Data          []byte    `json:"data,omitempty"` // JSON payload
}

type EventType string

const (
	EventTypeMessage   EventType = "message"
	EventTypeToolCall  EventType = "tool_call"
	EventTypeToolResult EventType = "tool_result"
	EventTypeSpawn     EventType = "spawn"
	EventTypeError     EventType = "error"
)

// ToolCall captures a specific tool invocation within an event.
type ToolCall struct {
	ID        string    `json:"id"`
	EventID   string    `json:"event_id"`
	RunID     string    `json:"run_id"`
	Tool      string    `json:"tool"`
	ArgsHash  string    `json:"args_hash"`
	DurationMs int64   `json:"duration_ms"`
	Success   bool      `json:"success"`
	Timestamp time.Time `json:"timestamp"`
}

// Alert represents a drift or anomaly detection result.
type Alert struct {
	ID        string     `json:"id"`
	RunID     *string    `json:"run_id,omitempty"`
	Type      AlertType  `json:"type"`
	Severity  Severity   `json:"severity"`
	Message   string     `json:"message"`
	Metric    string     `json:"metric"`
	Expected  float64    `json:"expected"`
	Actual    float64    `json:"actual"`
	ZScore    float64    `json:"z_score"`
	CreatedAt time.Time  `json:"created_at"`
	AckedAt   *time.Time `json:"acked_at,omitempty"`
}

type AlertType string

const (
	AlertTypeCostSpike    AlertType = "cost_spike"
	AlertTypeTokenAnomaly AlertType = "token_anomaly"
	AlertTypeLoopDetected AlertType = "loop_detected"
	AlertTypeDrift        AlertType = "drift"
	AlertTypeErrorSpike   AlertType = "error_spike"
)

type Severity string

const (
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)
