package sqlite

import (
	"database/sql"
	"fmt"
	"time"
)

type migration struct {
	version int
	sql     string
}

// migrations is the ordered list of schema migrations.
// Each migration is applied exactly once, tracked in schema_migrations.
var migrations = []migration{
	{
		version: 1,
		sql: `
CREATE TABLE IF NOT EXISTS runs (
    id               TEXT    PRIMARY KEY,
    parent_run_id    TEXT,
    framework        TEXT    NOT NULL,
    agent_id         TEXT    NOT NULL,
    session_key      TEXT    NOT NULL,
    status           TEXT    NOT NULL,
    started_at       INTEGER NOT NULL,
    completed_at     INTEGER,
    total_tokens_in  INTEGER NOT NULL DEFAULT 0,
    total_tokens_out INTEGER NOT NULL DEFAULT 0,
    total_cost_usd   REAL    NOT NULL DEFAULT 0,
    metadata         TEXT    NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_runs_framework  ON runs(framework);
CREATE INDEX IF NOT EXISTS idx_runs_status     ON runs(status);
CREATE INDEX IF NOT EXISTS idx_runs_started_at ON runs(started_at);

CREATE TABLE IF NOT EXISTS events (
    id              TEXT    PRIMARY KEY,
    run_id          TEXT    NOT NULL REFERENCES runs(id),
    parent_event_id TEXT,
    type            TEXT    NOT NULL,
    timestamp       INTEGER NOT NULL,
    duration_ms     INTEGER,
    tokens_in       INTEGER NOT NULL DEFAULT 0,
    tokens_out      INTEGER NOT NULL DEFAULT 0,
    cost_usd        REAL    NOT NULL DEFAULT 0,
    model           TEXT    NOT NULL DEFAULT '',
    data            BLOB
);

CREATE INDEX IF NOT EXISTS idx_events_run_id    ON events(run_id);
CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);
CREATE INDEX IF NOT EXISTS idx_events_type      ON events(type);

CREATE TABLE IF NOT EXISTS tool_calls (
    id          TEXT    PRIMARY KEY,
    event_id    TEXT    NOT NULL REFERENCES events(id),
    run_id      TEXT    NOT NULL REFERENCES runs(id),
    tool        TEXT    NOT NULL,
    args_hash   TEXT    NOT NULL DEFAULT '',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    success     INTEGER NOT NULL DEFAULT 0,
    timestamp   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tool_calls_run_id    ON tool_calls(run_id);
CREATE INDEX IF NOT EXISTS idx_tool_calls_tool      ON tool_calls(tool);
CREATE INDEX IF NOT EXISTS idx_tool_calls_timestamp ON tool_calls(timestamp);

CREATE TABLE IF NOT EXISTS alerts (
    id         TEXT    PRIMARY KEY,
    run_id     TEXT,
    type       TEXT    NOT NULL,
    severity   TEXT    NOT NULL,
    message    TEXT    NOT NULL,
    metric     TEXT    NOT NULL,
    expected   REAL    NOT NULL DEFAULT 0,
    actual     REAL    NOT NULL DEFAULT 0,
    z_score    REAL    NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    acked_at   INTEGER
);

CREATE INDEX IF NOT EXISTS idx_alerts_severity   ON alerts(severity);
CREATE INDEX IF NOT EXISTS idx_alerts_created_at ON alerts(created_at);
CREATE INDEX IF NOT EXISTS idx_alerts_run_id     ON alerts(run_id);

CREATE TABLE IF NOT EXISTS collector_state (
    collector_id  TEXT    PRIMARY KEY,
    file_path     TEXT    NOT NULL DEFAULT '',
    byte_offset   INTEGER NOT NULL DEFAULT 0,
    last_event_id TEXT    NOT NULL DEFAULT ''
);
`,
	},
}

// runMigrations bootstraps the schema_migrations table then applies any
// pending migrations in version order.
func runMigrations(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("bootstrap schema_migrations: %w", err)
	}

	for _, m := range migrations {
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.version,
		).Scan(&count); err != nil {
			return fmt.Errorf("check migration %d: %w", m.version, err)
		}
		if count > 0 {
			continue // already applied
		}

		if _, err := db.Exec(m.sql); err != nil {
			return fmt.Errorf("apply migration %d: %w", m.version, err)
		}

		if _, err := db.Exec(
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			m.version, time.Now().UnixMilli(),
		); err != nil {
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}
	}
	return nil
}
