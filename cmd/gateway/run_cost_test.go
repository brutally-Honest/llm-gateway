package main

import (
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/cost"
	"github.com/brutally-honest/llm-gateway/internal/protocols/anthropic"
)

// TestRegisterCostBooks_PricesTheAdaptersProtocol: after start the stored protocol name
// of the adapter's exchanges finds its price book (spec 020 AC10).
func TestRegisterCostBooks_PricesTheAdaptersProtocol(t *testing.T) {
	registerCostBooks()
	n := int64(1_000_000)
	c := cost.Price(cost.Usage{Protocol: anthropic.Name, Model: "claude-opus-5-5", At: time.Now(), Input: &n, Output: new(int64),
		CacheWrite: new(int64), CacheRead: new(int64)})
	if c.Status != cost.StatusPriced || cost.USD(c.Amount) != "4" || c.Source != "Anthropic" {
		t.Fatalf("cost = %+v, want priced $4 from Anthropic", c)
	}
}
