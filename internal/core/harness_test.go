package core_test

import (
	"strings"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// TestHarnessPhase_ValuesNameNoHarness: a phase is harness-neutral, so no value may
// contain a hook event name. Phases are written with underscores, so the
// check is on the joined lower-case word ("sessionstart" is not "session_start").
func TestHarnessPhase_ValuesNameNoHarness(t *testing.T) {
	phases := []core.HarnessPhase{
		core.PhasePreTool, core.PhasePermissionRequested, core.PhasePermissionDenied,
		core.PhasePostTool, core.PhasePostToolFailed, core.PhaseToolBatchDone,
		core.PhasePrompt, core.PhaseSessionStart, core.PhaseSessionEnd,
		core.PhaseTurnEnd, core.PhaseTurnFailed, core.PhaseAgentStart,
		core.PhaseAgentEnd, core.PhaseNotice, core.PhaseCompactStart,
		core.PhaseCompactEnd, core.PhaseUnknown,
	}
	for _, p := range phases {
		for _, name := range append(append([]string{}, harnessEventNames...), harnessQuotedNames...) {
			word := strings.ToLower(name)
			if strings.Contains(string(p), word) {
				t.Errorf("phase %q contains the hook event word %q", p, word)
			}
		}
	}
}
