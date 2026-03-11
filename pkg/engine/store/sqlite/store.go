// Package sqlite implements the engine.Store interface using SQLite (modernc,
// pure Go, no CGo). WAL mode, foreign keys ON, and 5 s busy_timeout are
// applied at open time. Schema migrations run automatically in New().
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Abaddollyon/spectr/pkg/engine"
	"github.com/google/uuid"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Store is the SQLite-backed implementation of engine.Store.
type Store struct {
	db *sql.DB
}

// Compile-time check: Store must satisfy engine.Store.
var _ engine.Store = (*Store)(nil)

// New opens (or creates) a SQLite database at dsn, applies PRAGMA settings,
// runs any pending migrations, and returns a ready-to-use Store.
//
// dsn may be a file path or ":memory:".
func New(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", dsn, err)
	}

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("pragma %q: %w", pragma, err)
		}
	}

	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrations: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error { return s.db.Close() }

// ─────────────────────────────────────────────────────────────
// Runs
// ─────────────────────────────────────────────────────────────

// InsertRun inserts a new run record. If run.ID is empty, a UUID is generated.
func (s *Store) InsertRun(ctx context.Context, run *engine.Run) error {
	if run.ID == "" {
		run.ID = uuid.New().String()
	}
	meta, err := jsonMarshal(run.Metadata)
	if err != nil {
		return fmt.Errorf("marshal run metadata: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO runs
			(id, parent_run_id, framework, agent_id, session_key, status,
			 started_at, completed_at,
			 total_tokens_in, total_tokens_out, total_cost_usd, metadata)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		run.ID, run.ParentRunID,
		run.Framework, run.AgentID, run.SessionKey, string(run.Status),
		run.StartedAt.UnixMilli(), timeToMillisPtr(run.CompletedAt),
		run.TotalTokensIn, run.TotalTokensOut, run.TotalCostUSD, meta,
	)
	return err
}

// UpdateRun updates mutable fields of an existing run (status, completion
// time, token counters, cost, metadata). ID must be set.
func (s *Store) UpdateRun(ctx context.Context, run *engine.Run) error {
	meta, err := jsonMarshal(run.Metadata)
	if err != nil {
		return fmt.Errorf("marshal run metadata: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE runs SET
			status           = ?,
			completed_at     = ?,
			total_tokens_in  = ?,
			total_tokens_out = ?,
			total_cost_usd   = ?,
			metadata         = ?
		WHERE id = ?`,
		string(run.Status), timeToMillisPtr(run.CompletedAt),
		run.TotalTokensIn, run.TotalTokensOut, run.TotalCostUSD,
		meta, run.ID,
	)
	return err
}

// GetRun returns the run with the given ID, or sql.ErrNoRows if absent.
func (s *Store) GetRun(ctx context.Context, id string) (*engine.Run, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, parent_run_id, framework, agent_id, session_key, status,
		       started_at, completed_at,
		       total_tokens_in, total_tokens_out, total_cost_usd, metadata
		FROM runs WHERE id = ?`, id)
	return scanRun(row)
}

// ListRuns returns runs filtered by the given options, newest first.
func (s *Store) ListRuns(ctx context.Context, opts engine.ListRunsOpts) ([]*engine.Run, error) {
	q := `
		SELECT id, parent_run_id, framework, agent_id, session_key, status,
		       started_at, completed_at,
		       total_tokens_in, total_tokens_out, total_cost_usd, metadata
		FROM runs WHERE 1=1`
	var args []any

	if opts.Framework != "" {
		q += " AND framework = ?"
		args = append(args, opts.Framework)
	}
	if opts.Status != "" {
		q += " AND status = ?"
		args = append(args, string(opts.Status))
	}
	if opts.Since != nil {
		q += " AND started_at >= ?"
		args = append(args, opts.Since.UnixMilli())
	}
	q += " ORDER BY started_at DESC"
	if opts.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, opts.Limit)
	}
	if opts.Offset > 0 {
		q += " OFFSET ?"
		args = append(args, opts.Offset)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*engine.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// ─────────────────────────────────────────────────────────────
// Events
// ─────────────────────────────────────────────────────────────

// InsertEvent inserts a new event record. If event.ID is empty, a UUID is
// generated.
func (s *Store) InsertEvent(ctx context.Context, event *engine.Event) error {
	if event.ID == "" {
		event.ID = uuid.New().String()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO events
			(id, run_id, parent_event_id, type, timestamp, duration_ms,
			 tokens_in, tokens_out, cost_usd, model, data)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		event.ID, event.RunID, event.ParentEventID,
		string(event.Type), event.Timestamp.UnixMilli(), event.DurationMs,
		event.TokensIn, event.TokensOut, event.CostUSD, event.Model, event.Data,
	)
	return err
}

// ListEvents returns all events for a run, ordered by timestamp ascending.
func (s *Store) ListEvents(ctx context.Context, runID string) ([]*engine.Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, run_id, parent_event_id, type, timestamp, duration_ms,
		       tokens_in, tokens_out, cost_usd, model, data
		FROM events WHERE run_id = ? ORDER BY timestamp ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*engine.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// ─────────────────────────────────────────────────────────────
// Tool calls
// ─────────────────────────────────────────────────────────────

// InsertToolCall inserts a tool call record. If tc.ID is empty, a UUID is
// generated.
func (s *Store) InsertToolCall(ctx context.Context, tc *engine.ToolCall) error {
	if tc.ID == "" {
		tc.ID = uuid.New().String()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tool_calls
			(id, event_id, run_id, tool, args_hash, duration_ms, success, timestamp)
		VALUES (?,?,?,?,?,?,?,?)`,
		tc.ID, tc.EventID, tc.RunID,
		tc.Tool, tc.ArgsHash, tc.DurationMs,
		boolToInt(tc.Success), tc.Timestamp.UnixMilli(),
	)
	return err
}

// ListToolCalls returns all tool calls for a run, ordered by timestamp
// ascending.
func (s *Store) ListToolCalls(ctx context.Context, runID string) ([]*engine.ToolCall, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_id, run_id, tool, args_hash, duration_ms, success, timestamp
		FROM tool_calls WHERE run_id = ? ORDER BY timestamp ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tcs []*engine.ToolCall
	for rows.Next() {
		tc, err := scanToolCall(rows)
		if err != nil {
			return nil, err
		}
		tcs = append(tcs, tc)
	}
	return tcs, rows.Err()
}

// ─────────────────────────────────────────────────────────────
// Alerts
// ─────────────────────────────────────────────────────────────

// InsertAlert inserts an alert record. If alert.ID is empty, a UUID is
// generated.
func (s *Store) InsertAlert(ctx context.Context, alert *engine.Alert) error {
	if alert.ID == "" {
		alert.ID = uuid.New().String()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO alerts
			(id, run_id, type, severity, message, metric,
			 expected, actual, z_score, created_at, acked_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		alert.ID, alert.RunID,
		string(alert.Type), string(alert.Severity),
		alert.Message, alert.Metric,
		alert.Expected, alert.Actual, alert.ZScore,
		alert.CreatedAt.UnixMilli(), timeToMillisPtr(alert.AckedAt),
	)
	return err
}

// ListAlerts returns alerts filtered by the given options, newest first.
func (s *Store) ListAlerts(ctx context.Context, opts engine.ListAlertsOpts) ([]*engine.Alert, error) {
	q := `
		SELECT id, run_id, type, severity, message, metric,
		       expected, actual, z_score, created_at, acked_at
		FROM alerts WHERE 1=1`
	var args []any

	if opts.Severity != "" {
		q += " AND severity = ?"
		args = append(args, string(opts.Severity))
	}
	if opts.AckedOnly {
		q += " AND acked_at IS NOT NULL"
	}
	if opts.Since != nil {
		q += " AND created_at >= ?"
		args = append(args, opts.Since.UnixMilli())
	}
	q += " ORDER BY created_at DESC"
	if opts.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, opts.Limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []*engine.Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

// AckAlert marks an alert as acknowledged at the current time.
func (s *Store) AckAlert(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE alerts SET acked_at = ? WHERE id = ?`,
		time.Now().UnixMilli(), id,
	)
	return err
}

// ─────────────────────────────────────────────────────────────
// Aggregations
// ─────────────────────────────────────────────────────────────

// TokenUsageOverTime returns total (in+out) tokens bucketed by interval.
// interval is a string like "1h", "30m", "1d".
func (s *Store) TokenUsageOverTime(ctx context.Context, from, to time.Time, interval string) ([]engine.TimeBucket, error) {
	dur, err := parseInterval(interval)
	if err != nil {
		return nil, fmt.Errorf("TokenUsageOverTime: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT timestamp, CAST(tokens_in + tokens_out AS REAL)
		FROM events
		WHERE timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp ASC`,
		from.UnixMilli(), to.UnixMilli(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return bucketRows(rows, from, to, dur)
}

// CostOverTime returns total cost_usd bucketed by interval.
func (s *Store) CostOverTime(ctx context.Context, from, to time.Time, interval string) ([]engine.TimeBucket, error) {
	dur, err := parseInterval(interval)
	if err != nil {
		return nil, fmt.Errorf("CostOverTime: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT timestamp, cost_usd
		FROM events
		WHERE timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp ASC`,
		from.UnixMilli(), to.UnixMilli(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return bucketRows(rows, from, to, dur)
}

// TopToolCalls returns the most-called tools (by count) within [from, to].
// limit defaults to 10 if ≤ 0.
func (s *Store) TopToolCalls(ctx context.Context, from, to time.Time, limit int) ([]engine.ToolFrequency, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT tool, COUNT(*) AS cnt
		FROM tool_calls
		WHERE timestamp >= ? AND timestamp <= ?
		GROUP BY tool
		ORDER BY cnt DESC
		LIMIT ?`,
		from.UnixMilli(), to.UnixMilli(), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var freqs []engine.ToolFrequency
	for rows.Next() {
		var f engine.ToolFrequency
		if err := rows.Scan(&f.Tool, &f.Count); err != nil {
			return nil, err
		}
		freqs = append(freqs, f)
	}
	return freqs, rows.Err()
}

// ─────────────────────────────────────────────────────────────
// Collector state
// ─────────────────────────────────────────────────────────────

// GetCollectorState returns the stored state for collectorID, or nil if none
// exists yet.
func (s *Store) GetCollectorState(ctx context.Context, collectorID string) (*engine.CollectorState, error) {
	var cs engine.CollectorState
	err := s.db.QueryRowContext(ctx, `
		SELECT collector_id, file_path, byte_offset, last_event_id
		FROM collector_state WHERE collector_id = ?`, collectorID,
	).Scan(&cs.CollectorID, &cs.FilePath, &cs.ByteOffset, &cs.LastEventID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cs, nil
}

// SetCollectorState upserts the collector state.
func (s *Store) SetCollectorState(ctx context.Context, state *engine.CollectorState) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO collector_state (collector_id, file_path, byte_offset, last_event_id)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(collector_id) DO UPDATE SET
			file_path     = excluded.file_path,
			byte_offset   = excluded.byte_offset,
			last_event_id = excluded.last_event_id`,
		state.CollectorID, state.FilePath, state.ByteOffset, state.LastEventID,
	)
	return err
}

// ─────────────────────────────────────────────────────────────
// Scan helpers
// ─────────────────────────────────────────────────────────────

// scannable is satisfied by both *sql.Row and *sql.Rows.
type scannable interface {
	Scan(dest ...any) error
}

func scanRun(s scannable) (*engine.Run, error) {
	var r engine.Run
	var parentRunID sql.NullString
	var completedAtMS sql.NullInt64
	var startedAtMS int64
	var metaJSON string

	err := s.Scan(
		&r.ID, &parentRunID,
		&r.Framework, &r.AgentID, &r.SessionKey, &r.Status,
		&startedAtMS, &completedAtMS,
		&r.TotalTokensIn, &r.TotalTokensOut, &r.TotalCostUSD, &metaJSON,
	)
	if err != nil {
		return nil, err
	}

	r.StartedAt = time.UnixMilli(startedAtMS).UTC()
	if parentRunID.Valid {
		r.ParentRunID = &parentRunID.String
	}
	if completedAtMS.Valid {
		t := time.UnixMilli(completedAtMS.Int64).UTC()
		r.CompletedAt = &t
	}
	if metaJSON != "" && metaJSON != "{}" && metaJSON != "null" {
		r.Metadata = make(map[string]string)
		if err := json.Unmarshal([]byte(metaJSON), &r.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal run metadata: %w", err)
		}
	}
	return &r, nil
}

func scanEvent(rows *sql.Rows) (*engine.Event, error) {
	var e engine.Event
	var parentEventID sql.NullString
	var durationMs sql.NullInt64
	var timestampMS int64

	err := rows.Scan(
		&e.ID, &e.RunID, &parentEventID,
		&e.Type, &timestampMS, &durationMs,
		&e.TokensIn, &e.TokensOut, &e.CostUSD, &e.Model, &e.Data,
	)
	if err != nil {
		return nil, err
	}
	e.Timestamp = time.UnixMilli(timestampMS).UTC()
	if parentEventID.Valid {
		e.ParentEventID = &parentEventID.String
	}
	if durationMs.Valid {
		e.DurationMs = &durationMs.Int64
	}
	return &e, nil
}

func scanToolCall(rows *sql.Rows) (*engine.ToolCall, error) {
	var tc engine.ToolCall
	var successInt int
	var timestampMS int64

	err := rows.Scan(
		&tc.ID, &tc.EventID, &tc.RunID,
		&tc.Tool, &tc.ArgsHash, &tc.DurationMs,
		&successInt, &timestampMS,
	)
	if err != nil {
		return nil, err
	}
	tc.Success = successInt != 0
	tc.Timestamp = time.UnixMilli(timestampMS).UTC()
	return &tc, nil
}

func scanAlert(rows *sql.Rows) (*engine.Alert, error) {
	var a engine.Alert
	var runID sql.NullString
	var ackedAtMS sql.NullInt64
	var createdAtMS int64

	err := rows.Scan(
		&a.ID, &runID,
		&a.Type, &a.Severity, &a.Message, &a.Metric,
		&a.Expected, &a.Actual, &a.ZScore,
		&createdAtMS, &ackedAtMS,
	)
	if err != nil {
		return nil, err
	}
	a.CreatedAt = time.UnixMilli(createdAtMS).UTC()
	if runID.Valid {
		a.RunID = &runID.String
	}
	if ackedAtMS.Valid {
		t := time.UnixMilli(ackedAtMS.Int64).UTC()
		a.AckedAt = &t
	}
	return &a, nil
}

// ─────────────────────────────────────────────────────────────
// Bucketing
// ─────────────────────────────────────────────────────────────

// bucketRows reads (timestampMS int64, value float64) rows and sums values
// into fixed-width time buckets aligned to from.
func bucketRows(rows *sql.Rows, from, to time.Time, interval time.Duration) ([]engine.TimeBucket, error) {
	intervalMS := interval.Milliseconds()
	if intervalMS <= 0 {
		return nil, fmt.Errorf("interval must be positive")
	}

	fromMS := from.UnixMilli()
	toMS := to.UnixMilli()

	// Pre-allocate all bucket slots in order.
	var starts []int64
	buckets := make(map[int64]float64)
	for t := fromMS; t < toMS; t += intervalMS {
		starts = append(starts, t)
		buckets[t] = 0
	}

	for rows.Next() {
		var tsMS int64
		var val float64
		if err := rows.Scan(&tsMS, &val); err != nil {
			return nil, err
		}
		// Align to bucket start.
		key := fromMS + ((tsMS-fromMS)/intervalMS)*intervalMS
		buckets[key] += val
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	result := make([]engine.TimeBucket, len(starts))
	for i, s := range starts {
		result[i] = engine.TimeBucket{
			Timestamp: time.UnixMilli(s).UTC(),
			Value:     buckets[s],
		}
	}
	return result, nil
}

// ─────────────────────────────────────────────────────────────
// Small utilities
// ─────────────────────────────────────────────────────────────

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// timeToMillisPtr converts *time.Time to *int64 (Unix ms), preserving nil.
func timeToMillisPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	ms := t.UnixMilli()
	return &ms
}

// jsonMarshal marshals v; returns "{}" for nil maps.
func jsonMarshal(v any) (string, error) {
	if v == nil {
		return "{}", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// parseInterval parses a human-readable interval string into a time.Duration.
// Supported suffixes: m (minute), h (hour), d (day).
// Examples: "1h", "30m", "7d".
func parseInterval(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid interval %q", s)
	}
	unit := s[len(s)-1]
	numStr := s[:len(s)-1]

	var n int
	if _, err := fmt.Sscanf(numStr, "%d", &n); err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid interval number in %q", s)
	}

	switch unit {
	case 'm':
		return time.Duration(n) * time.Minute, nil
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown interval unit %q in %q", string(unit), s)
	}
}
