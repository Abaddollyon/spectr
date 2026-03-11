package normalize

import (
	"encoding/json"
	"unicode/utf8"
)

// ─── Framework identifiers ────────────────────────────────────────────────────

const (
	// FrameworkOpenClaw identifies the OpenClaw runtime.
	// OpenClaw does not expose native token counts in its JSONL session files;
	// we estimate usage from raw message content length.
	FrameworkOpenClaw = "openclaw"

	// FrameworkClaudeCode identifies the Claude Code CLI harness.
	// Its transcript JSON contains an explicit "usage" block with token counts.
	FrameworkClaudeCode = "claudecode"

	// FrameworkCodex identifies the OpenAI Codex CLI harness.
	// It emits token_count events as a top-level JSON object.
	FrameworkCodex = "codexcli"
)

// ─── Token-count estimation ───────────────────────────────────────────────────

// charsPerToken is the empirical ratio used to convert UTF-8 character counts
// into an approximate token count for models that follow GPT-style BPE
// tokenisation.  The true ratio varies by content, but 4 chars/token is the
// widely-cited average for English prose and mixed code.
const charsPerToken = 4

// estimateTokens converts a byte slice of arbitrary text into a rough token
// count using the charsPerToken constant.
func estimateTokens(text []byte) int64 {
	chars := int64(utf8.RuneCount(text))
	tokens := chars / charsPerToken
	if tokens < 1 && chars > 0 {
		tokens = 1
	}
	return tokens
}

// ─── Per-framework raw shapes ─────────────────────────────────────────────────

// openclawMessage is a partial view of a single OpenClaw JSONL line.
// Only the fields relevant for cost estimation are decoded.
type openclawMessage struct {
	Type    string `json:"type"`
	Message *struct {
		Role    string `json:"role"`
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"content"`
	} `json:"message"`
}

// claudeCodePayload is the JSON shape that the Claude Code CLI emits for a
// completed assistant turn.  The usage block mirrors the Anthropic API schema.
type claudeCodePayload struct {
	Usage *struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

// codexEvent is the JSON shape of a token_count event from the Codex CLI.
type codexEvent struct {
	Type         string `json:"type"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	// Legacy aliases emitted by older Codex CLI builds.
	TokenCount      int64 `json:"token_count"`
	CompletionCount int64 `json:"completion_count"`
}

// ─── Public API ───────────────────────────────────────────────────────────────

// NormalizeTokens extracts (inputTokens, outputTokens) from a raw JSON payload
// whose shape depends on the producing framework.
//
// Supported framework values: "openclaw", "claudecode", "codexcli".
// Any unrecognised framework returns (0, 0) rather than an error — token data
// is advisory and must not break the ingestion pipeline.
//
// Negative counts are clamped to zero.
func NormalizeTokens(framework string, rawData json.RawMessage) (inputTokens, outputTokens int64) {
	switch framework {
	case FrameworkOpenClaw:
		return normalizeOpenClaw(rawData)
	case FrameworkClaudeCode:
		return normalizeClaudeCode(rawData)
	case FrameworkCodex:
		return normalizeCodex(rawData)
	default:
		return 0, 0
	}
}

// ─── Framework-specific parsers ───────────────────────────────────────────────

// normalizeOpenClaw estimates token usage from the character length of all
// text/thinking content in a single JSONL message line.
//
// Input tokens are estimated from the user message content.
// Output tokens are estimated from the assistant message content.
// Non-message lines (type != "message") produce (0, 0).
func normalizeOpenClaw(rawData json.RawMessage) (int64, int64) {
	if len(rawData) == 0 {
		return 0, 0
	}

	var msg openclawMessage
	if err := json.Unmarshal(rawData, &msg); err != nil {
		return 0, 0
	}
	if msg.Type != "message" || msg.Message == nil {
		return 0, 0
	}

	var total int64
	for _, c := range msg.Message.Content {
		switch c.Type {
		case "text":
			total += estimateTokens([]byte(c.Text))
		case "thinking":
			total += estimateTokens([]byte(c.Thinking))
		}
	}

	switch msg.Message.Role {
	case "user":
		return total, 0
	case "assistant":
		return 0, total
	default:
		// tool or unknown — treat as output
		return 0, total
	}
}

// normalizeClaudeCode reads the explicit usage block from a Claude Code turn
// JSON.  If the block is absent or malformed the function returns (0, 0).
func normalizeClaudeCode(rawData json.RawMessage) (int64, int64) {
	if len(rawData) == 0 {
		return 0, 0
	}

	var payload claudeCodePayload
	if err := json.Unmarshal(rawData, &payload); err != nil {
		return 0, 0
	}
	if payload.Usage == nil {
		return 0, 0
	}

	in := clamp(payload.Usage.InputTokens)
	out := clamp(payload.Usage.OutputTokens)
	return in, out
}

// normalizeCodex reads a Codex CLI token_count event.  The event must have
// type == "token_count"; all other event types produce (0, 0).
// Both the canonical (input_tokens / output_tokens) and the legacy
// (token_count / completion_count) field names are supported.
func normalizeCodex(rawData json.RawMessage) (int64, int64) {
	if len(rawData) == 0 {
		return 0, 0
	}

	var ev codexEvent
	if err := json.Unmarshal(rawData, &ev); err != nil {
		return 0, 0
	}
	if ev.Type != "token_count" {
		return 0, 0
	}

	// Prefer canonical fields; fall back to legacy aliases.
	in := ev.InputTokens
	if in == 0 {
		in = ev.TokenCount
	}
	out := ev.OutputTokens
	if out == 0 {
		out = ev.CompletionCount
	}

	return clamp(in), clamp(out)
}

// clamp ensures a token count is never negative.
func clamp(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
