// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package usage

import "strings"

// modelPricing is the per-million-token price for a model family.
// CachedPerMTok is the cached/cache-read input rate; 0 means "use the
// standard 0.1× input rate" (Anthropic's cache-read multiplier and OpenAI's
// usual 90%-off cached rate are both 0.1×). Claude cache *writes* use the
// multipliers below on the input rate; OpenAI has no cache-write charge.
type modelPricing struct {
	InputPerMTok  float64
	CachedPerMTok float64
	OutputPerMTok float64
}

// pricingTable maps model-id substrings to prices, checked in order — put
// more specific entries before more general ones. Prices are USD per million
// tokens. Unknown models contribute tokens but no cost.
var pricingTable = []struct {
	substr string
	p      modelPricing
}{
	// Anthropic (Claude Code)
	{"fable", modelPricing{10, 0, 50}},
	{"mythos", modelPricing{10, 0, 50}},
	{"opus-4-1", modelPricing{15, 0, 75}},
	{"opus-4-2025", modelPricing{15, 0, 75}}, // claude-opus-4-20250514
	{"opus-4-0", modelPricing{15, 0, 75}},
	{"opus", modelPricing{5, 0, 25}}, // Opus 4.5 and later
	{"sonnet", modelPricing{3, 0, 15}},
	{"claude-3-5-haiku", modelPricing{0.8, 0, 4}},
	{"claude-3-haiku", modelPricing{0.25, 0, 1.25}},
	{"haiku", modelPricing{1, 0, 5}}, // Haiku 4.5

	// OpenAI (Codex CLI) — codex variants (e.g. gpt-5.5-codex) fall through
	// to their base-version entry via substring match.
	{"gpt-5.6-sol", modelPricing{5, 0.5, 30}},  // flat across reasoning tiers
	{"gpt-5.5-pro", modelPricing{30, 30, 180}}, // no cached rate offered
	{"gpt-5.5", modelPricing{5, 0.5, 30}},
	{"gpt-5.4-pro", modelPricing{30, 30, 180}},
	{"gpt-5.4-mini", modelPricing{0.75, 0, 4.5}},
	{"gpt-5.4-nano", modelPricing{0.2, 0, 1.25}},
	{"gpt-5.4", modelPricing{2.5, 0.25, 15}},
	{"gpt-5.2-pro", modelPricing{21, 0, 168}},
	{"gpt-5.2", modelPricing{1.75, 0, 14}},
	{"gpt-5.1", modelPricing{1.25, 0, 10}},
	{"gpt-5-pro", modelPricing{15, 15, 120}},
	{"gpt-5-mini", modelPricing{0.25, 0, 2}},
	{"gpt-5-nano", modelPricing{0.05, 0, 0.4}},
	{"gpt-5", modelPricing{1.25, 0, 10}}, // gpt-5, gpt-5-codex, gpt-5.0
	{"codex-mini", modelPricing{1.5, 0.375, 6}},
	{"o4-mini", modelPricing{1.1, 0.275, 4.4}},
	{"o3-pro", modelPricing{20, 20, 80}},
	{"o3", modelPricing{2, 0.5, 8}},
}

const (
	cacheReadMultiplier    = 0.1
	cacheWrite5mMultiplier = 1.25
	cacheWrite1hMultiplier = 2.0
)

// pricingFor returns the pricing for a model id, and whether it was found.
func pricingFor(model string) (modelPricing, bool) {
	m := strings.ToLower(model)
	for _, row := range pricingTable {
		if strings.Contains(m, row.substr) {
			return row.p, true
		}
	}
	return modelPricing{}, false
}

// costFor computes the USD cost of an entry from its token counts.
func costFor(e Entry) float64 {
	p, ok := pricingFor(e.Model)
	if !ok {
		return 0
	}
	cachedRate := p.CachedPerMTok
	if cachedRate == 0 {
		cachedRate = cacheReadMultiplier * p.InputPerMTok
	}
	inputCost := p.InputPerMTok/1e6*(float64(e.Input)+
		cacheWrite5mMultiplier*float64(e.Cache5m)+
		cacheWrite1hMultiplier*float64(e.Cache1h)) +
		cachedRate/1e6*float64(e.CacheRead)
	outputCost := p.OutputPerMTok / 1e6 * float64(e.Output)
	return inputCost + outputCost
}
