package normalize

import (
	"math"
	"testing"
)

// tolerance for float64 comparison (one-tenth of a micro-dollar).
const delta = 1e-7

func approxEqual(t *testing.T, label string, want, got float64) {
	t.Helper()
	if math.Abs(want-got) > delta {
		t.Errorf("%s: want %.9f, got %.9f", label, want, got)
	}
}

// ─── CalculateCost ────────────────────────────────────────────────────────────

func TestCalculateCost_ClaudeSonnet46(t *testing.T) {
	// 1 000 input + 500 output
	// input:  1000 / 1000 * 0.003   = 0.003
	// output:  500 / 1000 * 0.015   = 0.0075
	// total                         = 0.0105
	got := CalculateCost("claude-sonnet-4-6", 1000, 500)
	approxEqual(t, "claude-sonnet-4-6", 0.0105, got)
}

func TestCalculateCost_ClaudeOpus46(t *testing.T) {
	// 2 000 input + 1 000 output
	// input:  2000 / 1000 * 0.005   = 0.010
	// output: 1000 / 1000 * 0.025   = 0.025
	// total                         = 0.035
	got := CalculateCost("claude-opus-4-6", 2000, 1000)
	approxEqual(t, "claude-opus-4-6", 0.035, got)
}

func TestCalculateCost_KimiK25_PublicAlias(t *testing.T) {
	// 10 000 input + 2 000 output
	// input:  10000 / 1000 * 0.00045 = 0.0045
	// output:  2000 / 1000 * 0.00220 = 0.0044
	// total                          = 0.0089
	got := CalculateCost("kimi-k2.5", 10000, 2000)
	approxEqual(t, "kimi-k2.5", 0.0089, got)
}

func TestCalculateCost_KimiK25_InternalAlias(t *testing.T) {
	// Same rates; using internal provider model id "k2p5".
	got := CalculateCost("k2p5", 10000, 2000)
	approxEqual(t, "k2p5", 0.0089, got)
}

func TestCalculateCost_GPT53Codex(t *testing.T) {
	// 4 000 input + 1 000 output
	// input:  4000 / 1000 * 0.00175 = 0.007
	// output: 1000 / 1000 * 0.014   = 0.014
	// total                         = 0.021
	got := CalculateCost("gpt-5.3-codex", 4000, 1000)
	approxEqual(t, "gpt-5.3-codex", 0.021, got)
}

func TestCalculateCost_Gemini31Pro(t *testing.T) {
	// 5 000 input + 2 000 output
	// input:  5000 / 1000 * 0.002  = 0.010
	// output: 2000 / 1000 * 0.012  = 0.024
	// total                        = 0.034
	got := CalculateCost("gemini-3.1-pro", 5000, 2000)
	approxEqual(t, "gemini-3.1-pro", 0.034, got)
}

func TestCalculateCost_UnknownModelReturnsZero(t *testing.T) {
	got := CalculateCost("gpt-99-ultra", 100000, 50000)
	if got != 0.0 {
		t.Errorf("unknown model: expected 0.0, got %f", got)
	}
}

func TestCalculateCost_EmptyModelReturnsZero(t *testing.T) {
	got := CalculateCost("", 1000, 1000)
	if got != 0.0 {
		t.Errorf("empty model: expected 0.0, got %f", got)
	}
}

func TestCalculateCost_ZeroTokens(t *testing.T) {
	// No tokens → no cost, even for a known model.
	got := CalculateCost("claude-opus-4-6", 0, 0)
	if got != 0.0 {
		t.Errorf("zero tokens: expected 0.0, got %f", got)
	}
}

func TestCalculateCost_OnlyInputTokens(t *testing.T) {
	// 1 000 input, 0 output for Sonnet → input cost only
	// 1000 / 1000 * 0.003 = 0.003
	got := CalculateCost("claude-sonnet-4-6", 1000, 0)
	approxEqual(t, "input-only", 0.003, got)
}

func TestCalculateCost_OnlyOutputTokens(t *testing.T) {
	// 0 input, 1 000 output for Sonnet → output cost only
	// 1000 / 1000 * 0.015 = 0.015
	got := CalculateCost("claude-sonnet-4-6", 0, 1000)
	approxEqual(t, "output-only", 0.015, got)
}

func TestCalculateCost_LargeTokenCount(t *testing.T) {
	// 1M input + 200K output for Opus — should not overflow or produce NaN.
	got := CalculateCost("claude-opus-4-6", 1_000_000, 200_000)
	// 1000000/1000*0.005 + 200000/1000*0.025 = 5.0 + 5.0 = 10.0
	approxEqual(t, "large-count", 10.0, got)
	if math.IsNaN(got) || math.IsInf(got, 0) {
		t.Errorf("unexpected NaN/Inf for large token count: %f", got)
	}
}

// Ensure both Kimi aliases resolve to identical costs.
func TestCalculateCost_KimiAliasesAreConsistent(t *testing.T) {
	a := CalculateCost("kimi-k2.5", 50000, 10000)
	b := CalculateCost("k2p5", 50000, 10000)
	if a != b {
		t.Errorf("kimi aliases diverge: kimi-k2.5=%f, k2p5=%f", a, b)
	}
}
