package anthropic_test

import (
	"bytes"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

// The usage event of a cut stream carries the Partial flag; the timeline reads it
// to mark a call's usage partial.
func TestParse_TruncatedStreamUsagePartial(t *testing.T) {
	full := readFixture(t, "stream.sse")
	cut := full[:bytes.Index(full, []byte(`" good"`))]
	res := parseStream(t, cut, "", true)
	for _, ev := range res.Events {
		if ev.Kind != core.KindUsage {
			continue
		}
		if !ev.Partial {
			t.Fatal("usage event of a cut stream is not flagged partial")
		}
		return
	}
	t.Fatal("no usage event in the cut stream")
}
