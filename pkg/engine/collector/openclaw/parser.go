// Package openclaw implements a Spectr Collector for OpenClaw JSONL session files.
// Each session file lives at ~/.openclaw/agents/<agentID>/sessions/<uuid>.jsonl and
// contains one JSON object per line, appended as the session progresses.
package openclaw

import (
	"encoding/json"
	"fmt"
	"time"
)

// ─── Raw JSONL types ──────────────────────────────────────────────────────────

// rawLine is the outermost envelope for every JSONL line in a session file.
type rawLine struct {
	// Common fields present on every line.
	Type      string `json:"type"`
	ID        string `json:"id"`
	ParentID  string `json:"parentId"`
	Timestamp string `json:"timestamp"`

	// type=session
	Version int    `json:"version"`
	CWD     string `json:"cwd"`

	// type=model_change
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`

	// type=custom
	CustomType string          `json:"customType"`
	Data       json.RawMessage `json:"data"`

	// type=message
	Message *rawMessage `json:"message"`
}

// rawMessage is the payload of a type=message line.
type rawMessage struct {
	Role    string       `json:"role"`
	Content []rawContent `json:"content"`

	// toolResult fields
	ToolCallID string `json:"toolCallId"`
	ToolName   string `json:"toolName"`
	IsError    bool   `json:"isError"`
}

// rawContent is a single item inside a message's content array.
type rawContent struct {
	Type string `json:"type"` // "text", "toolCall", "thinking"

	// type=text or type=thinking
	Text     string `json:"text"`
	Thinking string `json:"thinking"`

	// type=toolCall
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ─── Parsed entry ─────────────────────────────────────────────────────────────

// ParsedEntry is the normalised, typed result of parsing one JSONL line.
type ParsedEntry struct {
	// Line type: "session", "model_change", "thinking_level_change", "custom", "message".
	Type string

	// LineID is the value of the top-level "id" field (short hex).
	LineID string

	// Timestamp is the parsed UTC timestamp of the line.
	Timestamp time.Time

	// session fields
	CWD string

	// model_change fields
	Provider string
	ModelID  string

	// message payload (nil for non-message lines)
	RawMessage *rawMessage
}

// ─── Parser ───────────────────────────────────────────────────────────────────

// parseLine decodes a single JSONL line into a ParsedEntry.
// It returns an error for blank lines and malformed JSON.
func parseLine(data []byte) (*ParsedEntry, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty line")
	}

	var raw rawLine
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	if raw.Type == "" {
		return nil, fmt.Errorf("missing type field")
	}

	ts, _ := time.Parse(time.RFC3339Nano, raw.Timestamp)
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	entry := &ParsedEntry{
		Type:      raw.Type,
		LineID:    raw.ID,
		Timestamp: ts,
	}

	switch raw.Type {
	case "session":
		entry.CWD = raw.CWD

	case "model_change":
		entry.Provider = raw.Provider
		entry.ModelID = raw.ModelID

	case "message":
		if raw.Message == nil {
			return nil, fmt.Errorf("message line missing message payload")
		}
		entry.RawMessage = raw.Message
		// The outer ISO timestamp is authoritative; the embedded unix-ms
		// timestamp in some message payloads is redundant (same value) and
		// not used for event timing.
	}

	return entry, nil
}
