// Package claudecode implements a Spectr Collector for Claude Code JSONL session files.
// Each session file lives at ~/.claude/projects/<project>/<uuid>.jsonl and contains
// one JSON object per line, appended as the session progresses.
package claudecode

import (
	"encoding/json"
	"fmt"
	"time"
)

// ─── Raw JSONL types ──────────────────────────────────────────────────────────

// rawLine is the outermost envelope for every JSONL line in a Claude Code session file.
// Claude Code uses a flat structure where type, uuid, sessionId, cwd, etc. are top-level.
type rawLine struct {
	// Common fields present on most lines.
	Type      string `json:"type"`      // "user", "assistant", "file-history-snapshot", etc.
	UUID      string `json:"uuid"`      // per-message UUID
	SessionID string `json:"sessionId"` // session UUID (also in filename)
	Timestamp string `json:"timestamp"` // RFC3339

	// Top-level contextual fields.
	CWD        string `json:"cwd"`
	Version    string `json:"version"`
	GitBranch  string `json:"gitBranch"`
	ParentUUID string `json:"parentUuid"`

	// type=user or type=assistant: the actual message payload.
	Message *rawMessage `json:"message"`

	// type=assistant: additional metadata.
	RequestID string `json:"requestId"`
}

// rawMessage is the message payload inside a Claude Code JSONL line.
// For user lines, content may be a string or an array of content items.
// For assistant lines, content is always an array.
type rawMessage struct {
	Role    string           `json:"role"`
	Model   string           `json:"model"`
	ID      string           `json:"id"` // Anthropic message ID
	Content json.RawMessage  `json:"content"`
	Usage   *rawUsage        `json:"usage"`

	// Stop reason (present on the last streaming chunk of a message).
	StopReason string `json:"stop_reason"`
}

// rawUsage captures token counts from the Anthropic API usage block.
type rawUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// rawContentItem represents a single item in a message content array.
type rawContentItem struct {
	Type string `json:"type"` // "text", "thinking", "tool_use", "tool_result"

	// type=text / type=thinking
	Text     string `json:"text"`
	Thinking string `json:"thinking"`

	// type=tool_use (assistant calls a tool)
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`

	// type=tool_result (user returns a result)
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"` // may be string or array
	IsError   bool            `json:"is_error"`
}

// ─── Parsed entry ─────────────────────────────────────────────────────────────

// ParsedEntry is the normalised, typed result of parsing one JSONL line.
type ParsedEntry struct {
	// Line type: "user", "assistant", "file-history-snapshot", etc.
	Type string

	// UUID is the per-line message UUID.
	UUID string

	// SessionID is the Claude Code session identifier.
	SessionID string

	// ParentUUID links to the parent message in the conversation tree.
	ParentUUID string

	// Timestamp is the parsed UTC timestamp of the line.
	Timestamp time.Time

	// CWD is the working directory reported in the line.
	CWD string

	// Version is the Claude Code CLI version string.
	Version string

	// Model is the model used for assistant messages (empty for user messages).
	Model string

	// Role is "user" or "assistant".
	Role string

	// TextContent is the concatenation of all text content items.
	TextContent string

	// ToolUses are tool invocations in assistant messages.
	ToolUses []ParsedToolUse

	// ToolResults are tool results returned in user messages.
	ToolResults []ParsedToolResult

	// Usage holds token counts extracted from the message (nil if not present).
	Usage *ParsedUsage
}

// ParsedToolUse represents a single tool_use content item in an assistant message.
type ParsedToolUse struct {
	ID    string          // Anthropic tool call ID (e.g. "toolu_01...")
	Name  string          // Tool name
	Input json.RawMessage // Raw JSON input
}

// ParsedToolResult represents a tool_result content item in a user message.
type ParsedToolResult struct {
	ToolUseID string // Matches the corresponding ParsedToolUse.ID
	IsError   bool
}

// ParsedUsage holds token usage numbers from a message.
type ParsedUsage struct {
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
}

// TotalIn returns total input tokens (prompt + cache creation + cache read).
func (u *ParsedUsage) TotalIn() int64 {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
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
		Type:       raw.Type,
		UUID:       raw.UUID,
		SessionID:  raw.SessionID,
		ParentUUID: raw.ParentUUID,
		Timestamp:  ts,
		CWD:        raw.CWD,
		Version:    raw.Version,
	}

	// Only user and assistant lines have message payloads we care about.
	if raw.Type != "user" && raw.Type != "assistant" {
		return entry, nil
	}

	if raw.Message == nil {
		return entry, nil
	}

	msg := raw.Message
	entry.Role = msg.Role
	entry.Model = msg.Model

	if msg.Usage != nil {
		entry.Usage = &ParsedUsage{
			InputTokens:              msg.Usage.InputTokens,
			OutputTokens:             msg.Usage.OutputTokens,
			CacheCreationInputTokens: msg.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     msg.Usage.CacheReadInputTokens,
		}
	}

	// Parse content — may be a bare string (simple user message) or an array.
	if err := parseContent(msg.Content, entry); err != nil {
		// Non-fatal: return what we have.
		return entry, nil
	}

	return entry, nil
}

// parseContent handles the polymorphic content field.
// It can be a plain string or a JSON array of content items.
func parseContent(raw json.RawMessage, entry *ParsedEntry) error {
	if len(raw) == 0 {
		return nil
	}

	// Try as string first (simple user messages).
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			entry.TextContent = s
			return nil
		}
	}

	// Try as array.
	if raw[0] == '[' {
		var items []rawContentItem
		if err := json.Unmarshal(raw, &items); err != nil {
			return fmt.Errorf("parse content array: %w", err)
		}
		for _, item := range items {
			switch item.Type {
			case "text":
				if entry.TextContent != "" {
					entry.TextContent += "\n"
				}
				entry.TextContent += item.Text
			case "thinking":
				// Not tracked as a separate event.
			case "tool_use":
				entry.ToolUses = append(entry.ToolUses, ParsedToolUse{
					ID:    item.ID,
					Name:  item.Name,
					Input: item.Input,
				})
			case "tool_result":
				entry.ToolResults = append(entry.ToolResults, ParsedToolResult{
					ToolUseID: item.ToolUseID,
					IsError:   item.IsError,
				})
			}
		}
	}

	return nil
}
