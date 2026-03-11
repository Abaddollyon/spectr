package normalize

import (
	"encoding/json"
	"testing"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// ─── estimateTokens (internal, tested via openclaw path) ─────────────────────

func TestEstimateTokens_Empty(t *testing.T) {
	if got := estimateTokens([]byte{}); got != 0 {
		t.Errorf("empty: want 0, got %d", got)
	}
}

func TestEstimateTokens_ShortText(t *testing.T) {
	// 3 chars < 4 chars/token → should still return 1
	if got := estimateTokens([]byte("abc")); got != 1 {
		t.Errorf("3-char: want 1, got %d", got)
	}
}

func TestEstimateTokens_ExactlyFourChars(t *testing.T) {
	// "abcd" = 4 chars → 1 token
	if got := estimateTokens([]byte("abcd")); got != 1 {
		t.Errorf("4-char: want 1, got %d", got)
	}
}

func TestEstimateTokens_EightChars(t *testing.T) {
	// "abcdefgh" = 8 chars → 2 tokens
	if got := estimateTokens([]byte("abcdefgh")); got != 2 {
		t.Errorf("8-char: want 2, got %d", got)
	}
}

// ─── NormalizeTokens — unknown framework ─────────────────────────────────────

func TestNormalizeTokens_UnknownFramework(t *testing.T) {
	in, out := NormalizeTokens("novelruntime", raw(`{"anything":true}`))
	if in != 0 || out != 0 {
		t.Errorf("unknown framework: want (0,0), got (%d,%d)", in, out)
	}
}

func TestNormalizeTokens_EmptyFramework(t *testing.T) {
	in, out := NormalizeTokens("", raw(`{}`))
	if in != 0 || out != 0 {
		t.Errorf("empty framework: want (0,0), got (%d,%d)", in, out)
	}
}

// ─── OpenClaw ────────────────────────────────────────────────────────────────

func TestNormalizeTokens_OpenClaw_UserMessage(t *testing.T) {
	// 8-char user message → 2 estimated input tokens, 0 output.
	payload := `{
		"type": "message",
		"message": {
			"role": "user",
			"content": [{"type": "text", "text": "abcdefgh"}]
		}
	}`
	in, out := NormalizeTokens(FrameworkOpenClaw, raw(payload))
	if in != 2 {
		t.Errorf("user message: want inputTokens=2, got %d", in)
	}
	if out != 0 {
		t.Errorf("user message: want outputTokens=0, got %d", out)
	}
}

func TestNormalizeTokens_OpenClaw_AssistantMessage(t *testing.T) {
	// 4-char assistant text → 0 input, 1 estimated output token.
	payload := `{
		"type": "message",
		"message": {
			"role": "assistant",
			"content": [{"type": "text", "text": "done"}]
		}
	}`
	in, out := NormalizeTokens(FrameworkOpenClaw, raw(payload))
	if in != 0 {
		t.Errorf("assistant: want inputTokens=0, got %d", in)
	}
	if out != 1 {
		t.Errorf("assistant: want outputTokens=1, got %d", out)
	}
}

func TestNormalizeTokens_OpenClaw_ThinkingContent(t *testing.T) {
	// Thinking block on assistant message: 8 chars → 2 tokens.
	payload := `{
		"type": "message",
		"message": {
			"role": "assistant",
			"content": [{"type": "thinking", "thinking": "thoughts!"}]
		}
	}`
	in, out := NormalizeTokens(FrameworkOpenClaw, raw(payload))
	if in != 0 {
		t.Errorf("thinking: want inputTokens=0, got %d", in)
	}
	// "thoughts!" = 9 chars → 9/4 = 2 tokens
	if out != 2 {
		t.Errorf("thinking: want outputTokens=2, got %d", out)
	}
}

func TestNormalizeTokens_OpenClaw_MultipleContentBlocks(t *testing.T) {
	// Two text blocks on a user message: "aaaabbbb" (4) + "ccccdddd" (8) = 12 chars → 3 tokens.
	payload := `{
		"type": "message",
		"message": {
			"role": "user",
			"content": [
				{"type": "text", "text": "aaaa"},
				{"type": "text", "text": "bbbbcccc"}
			]
		}
	}`
	in, out := NormalizeTokens(FrameworkOpenClaw, raw(payload))
	// "aaaa"=4→1, "bbbbcccc"=8→2  → total 3
	if in != 3 {
		t.Errorf("multi-block: want inputTokens=3, got %d", in)
	}
	if out != 0 {
		t.Errorf("multi-block: want outputTokens=0, got %d", out)
	}
}

func TestNormalizeTokens_OpenClaw_NonMessageType(t *testing.T) {
	// model_change line: not a message → (0, 0)
	payload := `{"type": "model_change", "modelId": "claude-sonnet-4-6"}`
	in, out := NormalizeTokens(FrameworkOpenClaw, raw(payload))
	if in != 0 || out != 0 {
		t.Errorf("non-message: want (0,0), got (%d,%d)", in, out)
	}
}

func TestNormalizeTokens_OpenClaw_NilMessage(t *testing.T) {
	payload := `{"type": "message"}`
	in, out := NormalizeTokens(FrameworkOpenClaw, raw(payload))
	if in != 0 || out != 0 {
		t.Errorf("nil message: want (0,0), got (%d,%d)", in, out)
	}
}

func TestNormalizeTokens_OpenClaw_EmptyData(t *testing.T) {
	in, out := NormalizeTokens(FrameworkOpenClaw, raw(""))
	if in != 0 || out != 0 {
		t.Errorf("empty data: want (0,0), got (%d,%d)", in, out)
	}
}

func TestNormalizeTokens_OpenClaw_InvalidJSON(t *testing.T) {
	in, out := NormalizeTokens(FrameworkOpenClaw, raw("{bad json"))
	if in != 0 || out != 0 {
		t.Errorf("invalid JSON: want (0,0), got (%d,%d)", in, out)
	}
}

func TestNormalizeTokens_OpenClaw_UnknownRole(t *testing.T) {
	// Unknown role treated as output.
	payload := `{
		"type": "message",
		"message": {
			"role": "tool",
			"content": [{"type": "text", "text": "result!!"}]
		}
	}`
	in, out := NormalizeTokens(FrameworkOpenClaw, raw(payload))
	if in != 0 {
		t.Errorf("unknown role: want inputTokens=0, got %d", in)
	}
	if out != 2 { // "result!!" = 8 chars → 2 tokens
		t.Errorf("unknown role: want outputTokens=2, got %d", out)
	}
}

// ─── Claude Code ─────────────────────────────────────────────────────────────

func TestNormalizeTokens_ClaudeCode_Standard(t *testing.T) {
	payload := `{
		"usage": {
			"input_tokens": 1500,
			"output_tokens": 350
		}
	}`
	in, out := NormalizeTokens(FrameworkClaudeCode, raw(payload))
	if in != 1500 {
		t.Errorf("claudecode: want inputTokens=1500, got %d", in)
	}
	if out != 350 {
		t.Errorf("claudecode: want outputTokens=350, got %d", out)
	}
}

func TestNormalizeTokens_ClaudeCode_ZeroTokens(t *testing.T) {
	payload := `{"usage": {"input_tokens": 0, "output_tokens": 0}}`
	in, out := NormalizeTokens(FrameworkClaudeCode, raw(payload))
	if in != 0 || out != 0 {
		t.Errorf("claudecode zero: want (0,0), got (%d,%d)", in, out)
	}
}

func TestNormalizeTokens_ClaudeCode_MissingUsageBlock(t *testing.T) {
	// JSON without "usage" → (0, 0)
	payload := `{"model": "claude-sonnet-4-6", "role": "assistant"}`
	in, out := NormalizeTokens(FrameworkClaudeCode, raw(payload))
	if in != 0 || out != 0 {
		t.Errorf("claudecode no-usage: want (0,0), got (%d,%d)", in, out)
	}
}

func TestNormalizeTokens_ClaudeCode_NegativeCountsClamped(t *testing.T) {
	payload := `{"usage": {"input_tokens": -100, "output_tokens": -50}}`
	in, out := NormalizeTokens(FrameworkClaudeCode, raw(payload))
	if in != 0 || out != 0 {
		t.Errorf("claudecode negative: want (0,0), got (%d,%d)", in, out)
	}
}

func TestNormalizeTokens_ClaudeCode_EmptyData(t *testing.T) {
	in, out := NormalizeTokens(FrameworkClaudeCode, raw(""))
	if in != 0 || out != 0 {
		t.Errorf("claudecode empty: want (0,0), got (%d,%d)", in, out)
	}
}

func TestNormalizeTokens_ClaudeCode_InvalidJSON(t *testing.T) {
	in, out := NormalizeTokens(FrameworkClaudeCode, raw("not-json"))
	if in != 0 || out != 0 {
		t.Errorf("claudecode invalid JSON: want (0,0), got (%d,%d)", in, out)
	}
}

// ─── Codex CLI ───────────────────────────────────────────────────────────────

func TestNormalizeTokens_Codex_Standard(t *testing.T) {
	payload := `{
		"type": "token_count",
		"input_tokens": 800,
		"output_tokens": 200
	}`
	in, out := NormalizeTokens(FrameworkCodex, raw(payload))
	if in != 800 {
		t.Errorf("codex: want inputTokens=800, got %d", in)
	}
	if out != 200 {
		t.Errorf("codex: want outputTokens=200, got %d", out)
	}
}

func TestNormalizeTokens_Codex_LegacyFields(t *testing.T) {
	// Older Codex CLI emits token_count / completion_count instead of
	// input_tokens / output_tokens.
	payload := `{
		"type": "token_count",
		"token_count": 1200,
		"completion_count": 400
	}`
	in, out := NormalizeTokens(FrameworkCodex, raw(payload))
	if in != 1200 {
		t.Errorf("codex legacy: want inputTokens=1200, got %d", in)
	}
	if out != 400 {
		t.Errorf("codex legacy: want outputTokens=400, got %d", out)
	}
}

func TestNormalizeTokens_Codex_CanonicalPreferredOverLegacy(t *testing.T) {
	// When both canonical and legacy fields are present, canonical wins.
	payload := `{
		"type": "token_count",
		"input_tokens": 500,
		"output_tokens": 100,
		"token_count": 9999,
		"completion_count": 9999
	}`
	in, out := NormalizeTokens(FrameworkCodex, raw(payload))
	if in != 500 {
		t.Errorf("codex canonical: want inputTokens=500, got %d", in)
	}
	if out != 100 {
		t.Errorf("codex canonical: want outputTokens=100, got %d", out)
	}
}

func TestNormalizeTokens_Codex_WrongType(t *testing.T) {
	// type != "token_count" → (0, 0)
	payload := `{"type": "tool_call", "input_tokens": 800, "output_tokens": 200}`
	in, out := NormalizeTokens(FrameworkCodex, raw(payload))
	if in != 0 || out != 0 {
		t.Errorf("codex wrong type: want (0,0), got (%d,%d)", in, out)
	}
}

func TestNormalizeTokens_Codex_NegativeCountsClamped(t *testing.T) {
	payload := `{"type": "token_count", "input_tokens": -1, "output_tokens": -1}`
	in, out := NormalizeTokens(FrameworkCodex, raw(payload))
	if in != 0 || out != 0 {
		t.Errorf("codex negative: want (0,0), got (%d,%d)", in, out)
	}
}

func TestNormalizeTokens_Codex_EmptyData(t *testing.T) {
	in, out := NormalizeTokens(FrameworkCodex, raw(""))
	if in != 0 || out != 0 {
		t.Errorf("codex empty: want (0,0), got (%d,%d)", in, out)
	}
}

func TestNormalizeTokens_Codex_InvalidJSON(t *testing.T) {
	in, out := NormalizeTokens(FrameworkCodex, raw("{!"))
	if in != 0 || out != 0 {
		t.Errorf("codex invalid JSON: want (0,0), got (%d,%d)", in, out)
	}
}

// ─── End-to-end: cost calculation from normalized tokens ─────────────────────

func TestEndToEnd_ClaudeCodeCost(t *testing.T) {
	payload := `{"usage": {"input_tokens": 2000, "output_tokens": 500}}`
	in, out := NormalizeTokens(FrameworkClaudeCode, raw(payload))
	cost := CalculateCost("claude-sonnet-4-6", in, out)
	// 2000/1000*0.003 + 500/1000*0.015 = 0.006 + 0.0075 = 0.0135
	approxEqual(t, "e2e claude-code cost", 0.0135, cost)
}

func TestEndToEnd_CodexCost(t *testing.T) {
	payload := `{"type": "token_count", "input_tokens": 4000, "output_tokens": 1000}`
	in, out := NormalizeTokens(FrameworkCodex, raw(payload))
	cost := CalculateCost("gpt-5.3-codex", in, out)
	// 4000/1000*0.00175 + 1000/1000*0.014 = 0.007 + 0.014 = 0.021
	approxEqual(t, "e2e codex cost", 0.021, cost)
}
