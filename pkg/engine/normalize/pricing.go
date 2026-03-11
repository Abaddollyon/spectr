// Package normalize provides token normalisation and cost calculation utilities
// for the Spectr agent observability engine. It is the single source of truth
// for model pricing and for translating framework-specific token payloads into
// the standard (inputTokens, outputTokens) pair used by engine.Event.
package normalize

// modelPricing holds the per-1 000-token rates for a single model.
// Using 1k-token granularity avoids floating-point precision surprises when
// working with the smallest practical billing unit.
type modelPricing struct {
	// InputPer1k is the USD cost per 1 000 input tokens.
	InputPer1k float64
	// OutputPer1k is the USD cost per 1 000 output tokens.
	OutputPer1k float64
}

// priceTable is the authoritative map of model name → billing rates.
// Prices are derived from official / OpenRouter listings as of March 2026.
// Add new models here; no other file needs to change.
//
// Rate sources (all standard, non-batch, non-cached):
//
//	claude-sonnet-4-6  Anthropic docs:   $3.00 / $15.00 per MTok
//	claude-opus-4-6    Anthropic docs:   $5.00 / $25.00 per MTok
//	kimi-k2.5          OpenRouter:       $0.45 /  $2.20 per MTok
//	gpt-5.3-codex      OpenRouter:       $1.75 / $14.00 per MTok
//	gemini-3.1-pro     Google / nxcode:  $2.00 / $12.00 per MTok
var priceTable = map[string]modelPricing{
	// ── Anthropic ──────────────────────────────────────────────────────────
	"claude-sonnet-4-6": {InputPer1k: 0.003, OutputPer1k: 0.015},
	"claude-opus-4-6":   {InputPer1k: 0.005, OutputPer1k: 0.025},

	// ── Moonshot / Kimi ────────────────────────────────────────────────────
	// "k2p5" is the internal model identifier used by the kimi-coding provider.
	// "kimi-k2.5" is the public/OpenRouter identifier for the same model.
	"kimi-k2.5": {InputPer1k: 0.00045, OutputPer1k: 0.00220},
	"k2p5":      {InputPer1k: 0.00045, OutputPer1k: 0.00220},

	// ── OpenAI Codex ───────────────────────────────────────────────────────
	"gpt-5.3-codex": {InputPer1k: 0.00175, OutputPer1k: 0.014},

	// ── Google Gemini ──────────────────────────────────────────────────────
	"gemini-3.1-pro": {InputPer1k: 0.002, OutputPer1k: 0.012},
}

// CalculateCost returns the estimated USD cost for a model call given the
// number of input and output tokens consumed.
//
// An unrecognised model name returns 0.0 — the caller should not fail; cost
// data is advisory and should degrade gracefully.
func CalculateCost(model string, inputTokens, outputTokens int64) float64 {
	p, ok := priceTable[model]
	if !ok {
		return 0.0
	}
	inputCost := float64(inputTokens) / 1000.0 * p.InputPer1k
	outputCost := float64(outputTokens) / 1000.0 * p.OutputPer1k
	return inputCost + outputCost
}
