package timeline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// The match states of a tool call, and of a hook event that matches none.
const (
	StateAskedRanReported = "asked+ran+reported"
	StateRanNotReported   = "ran-not-reported"
	StateAskedNotRun      = "asked-not-run"
	StatePending          = "pending"
	StateNoHarnessData    = "no harness data"
	StateUnmatched        = "ran-not-asked-or-not-captured"
)

// The reasons of StateAskedNotRun.
const (
	ReasonDenied = "denied"
	ReasonNotRun = "not run"
)

// The outcomes of a tool call that ran.
const (
	OutcomeSucceeded = "succeeded"
	OutcomeFailed    = "failed"
)

// dupWindow is how close two byte-identical payloads without a tool-use id must be
// received for the later one to count as a repeat.
const dupWindowNS = int64(1_000_000_000)

// phaseRank orders the phases of one tool call; timestamps never decide a state.
func phaseRank(p string) int {
	switch core.HarnessPhase(p) {
	case core.PhasePreTool:
		return 0
	case core.PhasePermissionRequested:
		return 1
	case core.PhasePermissionDenied:
		return 2
	case core.PhasePostTool:
		return 3
	case core.PhasePostToolFailed:
		return 4
	}
	return -1
}

// sortHooks gives hook rows one order whatever order they were read in.
func sortHooks(rows []store.HookRow) []store.HookRow {
	out := append([]store.HookRow(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.ReceivedAt != b.ReceivedAt {
			return a.ReceivedAt < b.ReceivedAt
		}
		if a.Seq != b.Seq {
			return a.Seq < b.Seq
		}
		if ra, rb := phaseRank(a.Phase), phaseRank(b.Phase); ra != rb {
			return ra < rb
		}
		return a.PayloadHash < b.PayloadHash
	})
	return out
}

// dedupHooks drops a later copy of a byte-identical payload when it carries a
// tool-use id or arrived within a second of the copy kept before it.
func dedupHooks(sorted []store.HookRow) []store.HookRow {
	var out []store.HookRow
	last := map[string]int64{} // payload hash -> receive time of the latest kept copy
	for _, h := range sorted {
		if at, ok := last[h.PayloadHash]; ok && (h.ToolUseID != "" || h.ReceivedAt-at <= dupWindowNS) {
			continue
		}
		last[h.PayloadHash] = h.ReceivedAt
		out = append(out, h)
	}
	return out
}

type callInfo struct {
	firstEx   int    // index of the first exchange holding the call
	inputHash string // content of the captured tool call input
}

// applyHarness adds match states to the tool-call entries and appends an entry for
// each hook event that matches no captured tool call. exchangeEvents holds each
// exchange's events in exchange order.
func applyHarness(src Source, session store.SessionRow, exchangeEvents [][]store.EventRow, out []Entry, opts Options) ([]Entry, error) {
	rows, err := src.HookEvents(session.PrincipalID, session.Client, session.SessionKey)
	if err != nil {
		return nil, fmt.Errorf("timeline: hook events: %w", err)
	}
	var exact []store.HookRow
	for _, h := range rows {
		// The session is principal + client + key; the key is an exact string.
		if h.PrincipalID == session.PrincipalID && h.Client == session.Client && h.SessionKey == session.SessionKey {
			exact = append(exact, h)
		}
	}
	hooks := dedupHooks(sortHooks(exact))

	calls := map[string]*callInfo{}
	resultAt := map[string][]int{} // tool call id -> exchanges holding a tool_result for it
	for i, evs := range exchangeEvents {
		for _, e := range evs {
			if e.ToolCallID == "" {
				continue
			}
			switch e.Kind {
			case "tool_call":
				if c, ok := calls[e.ToolCallID]; !ok {
					calls[e.ToolCallID] = &callInfo{firstEx: i, inputHash: e.ContentHash}
				} else if c.inputHash == "" {
					c.inputHash = e.ContentHash
				}
			case "tool_result":
				resultAt[e.ToolCallID] = append(resultAt[e.ToolCallID], i)
			}
		}
	}

	byID := map[string][]store.HookRow{}
	for _, h := range hooks {
		if h.ToolUseID != "" {
			byID[h.ToolUseID] = append(byID[h.ToolUseID], h)
		}
	}

	verdicts := map[string]Entry{}
	for id, c := range calls {
		v, err := verdict(src, hooks, byID[id], c, resultAt[id], opts)
		if err != nil {
			return nil, err
		}
		verdicts[id] = v
	}
	for i := range out {
		if out[i].Kind != KindToolCall {
			continue
		}
		v, ok := verdicts[out[i].ToolCallID]
		if !ok {
			continue
		}
		out[i].State, out[i].Reason, out[i].Outcome = v.State, v.Reason, v.Outcome
		out[i].InputDiffers, out[i].HookInput = v.InputDiffers, v.HookInput
		out[i].AgentID, out[i].AgentType = v.AgentID, v.AgentType
	}

	var loose []Entry // in (received_at, seq) order
	for _, h := range hooks {
		if _, matched := calls[h.ToolUseID]; matched && h.ToolUseID != "" {
			continue
		}
		en := Entry{Kind: KindHarness, Start: h.ReceivedAt, ToolCallID: h.ToolUseID, Name: h.ToolName,
			Phase: h.Phase, Text: h.SourceName, AgentID: h.AgentID, AgentType: h.AgentType, HookID: h.ID}
		if h.ToolUseID != "" {
			en.State = StateUnmatched
		}
		loose = append(loose, en)
	}
	// Merge by time: a hook event goes before the first exchange that started after
	// it was received; on a tie the exchange comes first.
	merged := make([]Entry, 0, len(out)+len(loose))
	for i, e := range out {
		if i == 0 || e.RequestID != out[i-1].RequestID {
			for len(loose) > 0 && loose[0].Start < e.Start {
				merged = append(merged, loose[0])
				loose = loose[1:]
			}
		}
		merged = append(merged, e)
	}
	return append(merged, loose...), nil
}

// verdict derives one tool call's state from the session's hook rows (ordered, deduped).
func verdict(src Source, all, mine []store.HookRow, c *callInfo, results []int, opts Options) (Entry, error) {
	var v Entry
	if len(all) == 0 {
		v.State = StateNoHarnessData
		return v, nil
	}
	rank := -1
	var pre *store.HookRow
	for i := range mine {
		h := &mine[i]
		if r := phaseRank(h.Phase); r > rank {
			rank = r
		}
		if core.HarnessPhase(h.Phase) == core.PhasePreTool && pre == nil {
			pre = h
		}
		if v.AgentID == "" {
			v.AgentID = h.AgentID
		}
		if v.AgentType == "" {
			v.AgentType = h.AgentType
		}
	}
	reported := len(results) > 0
	later := false
	for _, ex := range results {
		if ex > c.firstEx {
			later = true
		}
	}
	switch {
	case rank >= phaseRank(string(core.PhasePostTool)):
		v.State = StateRanNotReported
		if reported {
			v.State = StateAskedRanReported
		}
		v.Outcome = OutcomeSucceeded
		if rank == phaseRank(string(core.PhasePostToolFailed)) {
			v.Outcome = OutcomeFailed
		}
	case rank == phaseRank(string(core.PhasePermissionDenied)):
		v.State, v.Reason = StateAskedNotRun, ReasonDenied
	case later:
		v.State, v.Reason = StateAskedNotRun, ReasonNotRun
	default:
		v.State = StatePending
	}
	if pre != nil && pre.InputHash != "" && c.inputHash != "" {
		hin, err := content(src, pre.InputHash)
		if err != nil {
			return v, err
		}
		cin, err := content(src, c.inputHash)
		if err != nil {
			return v, err
		}
		if hin != nil && cin != nil && !sameJSON(hin, cin) {
			v.InputDiffers = true
			v.HookInput = string(hin)
			if !opts.Full {
				v.HookInput, _ = cut(v.HookInput, opts.limit())
			}
		}
	}
	return v, nil
}

// sameJSON compares two inputs in the canonical form: keys sorted, numbers as
// written. Input that is not JSON compares as bytes.
func sameJSON(a, b []byte) bool {
	ca, okA := canonicalJSON(a)
	cb, okB := canonicalJSON(b)
	if !okA || !okB {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(ca, cb)
}

func canonicalJSON(raw []byte) ([]byte, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil || dec.More() {
		return nil, false
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}
