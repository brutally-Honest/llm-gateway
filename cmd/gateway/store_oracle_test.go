//go:build storeoracle

package main

import (
	"os"
	"sort"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/ui"
)

// TestStoreOracle replays every session of a real store copy through the incremental and
// the full build (spec 040 AC3). It reads the copy in GATEWAY_STORE_ORACLE_DIR and never
// writes to it.
func TestStoreOracle(t *testing.T) {
	dir := os.Getenv("GATEWAY_STORE_ORACLE_DIR")
	if dir == "" {
		t.Skip("GATEWAY_STORE_ORACLE_DIR is not set")
	}
	rep, err := ui.RunStoreOracle(dir, uiRules(), t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(rep.Builds))
	for k := range rep.Builds {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	t.Logf("sessions %d (largest %d exchanges), inputs %d, full comparisons %d, differences %d, unassigned passthrough %v",
		rep.Sessions, rep.Largest, rep.Inputs, rep.FullComparisons, len(rep.Differences), rep.UnassignedPassthrough)
	for _, k := range keys {
		t.Logf("builds %s: %d", k, rep.Builds[k])
	}
	for _, d := range rep.Differences {
		t.Errorf("session %s (%d exchanges) step %d (%s): %s", d.Session, d.Exchanges, d.Step, d.Input, d.Detail)
	}
}
