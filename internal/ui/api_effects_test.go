package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// AC13, AC15: a session's file effects come back in id order and page with after; a file
// with two versions gives the expected diff; a binary file's diff is a 404 with a reason.
func TestEffectsAPI_ListAndDiff(t *testing.T) {
	e := newAPIEnv(t)
	id := store.SessionIdent{Principal: core.PrincipalLocal, Client: "client-a", Key: "s1"}
	v1, v2 := []byte("one\ntwo\nthree\n"), []byte("one\nTWO\nthree\n")
	change := func(path string, c core.FileChange, at int, after, before []byte) core.Effect {
		ef := core.Effect{Kind: core.EffectFileChange, At: baseTime.Add(time.Duration(at) * time.Second),
			Principal: id.Principal, Client: id.Client, SessionKey: id.Key, Root: "/w", Path: path,
			Change: c, Size: int64(len(after)), Mode: "git"}
		if after != nil {
			ef.AfterHash = store.ContentHash(after)
		}
		if before != nil {
			ef.BeforeHash = store.ContentHash(before)
		}
		return ef
	}
	cp := store.Checkpoint{
		Effects: []core.Effect{
			change("/w/a.txt", core.FileCreated, 1, v1, nil),
			change("/w/a.txt", core.FileModified, 2, v2, v1),
			change("/w/img.png", core.FileCreated, 3, nil, nil),
		},
		Copies: [][]byte{v1, v2},
	}
	if err := e.s.SaveCheckpoint(t.Context(), id, cp); err != nil {
		t.Fatal(err)
	}
	sid := encodeID(t, id.Principal, id.Client, id.Key)

	type page struct {
		Effects []effectJSON       `json:"effects"`
		Version *effectVersionJSON `json:"version"`
	}
	list := func(path string) page {
		t.Helper()
		rec := e.get(path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body)
		}
		var p page
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	all := list("/api/sessions/" + sid + "/effects")
	if len(all.Effects) != 3 || all.Version == nil || all.Version.Count != 3 {
		t.Fatalf("effects = %+v, version %+v; want 3", all.Effects, all.Version)
	}
	for i := 1; i < len(all.Effects); i++ {
		if all.Effects[i].ID <= all.Effects[i-1].ID {
			t.Errorf("effects not in id order: %d after %d", all.Effects[i].ID, all.Effects[i-1].ID)
		}
	}
	first, mod, bin := all.Effects[0], all.Effects[1], all.Effects[2]
	if first.HasDiff || !first.HasCopy || !mod.HasDiff || bin.HasCopy || bin.HasDiff {
		t.Errorf("has_copy/has_diff = %+v %+v %+v", first, mod, bin)
	}
	rest := list(fmt.Sprintf("/api/sessions/%s/effects?after=%d", sid, first.ID))
	if len(rest.Effects) != 2 || rest.Effects[0].ID != mod.ID {
		t.Errorf("after=%d gave %+v", first.ID, rest.Effects)
	}

	rec := e.get(fmt.Sprintf("/api/effects/%d/diff", mod.ID))
	var d diffJSON
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &d) != nil {
		t.Fatalf("diff = %d %s", rec.Code, rec.Body)
	}
	if want := "@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n"; d.Diff != want {
		t.Errorf("diff = %q, want %q", d.Diff, want)
	}

	rec = e.get(fmt.Sprintf("/api/effects/%d/diff", bin.ID))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "binary") {
		t.Errorf("binary diff = %d %s, want 404 with a reason", rec.Code, rec.Body)
	}
}

// attrTestRules adds effect attribution to testRules: an input names a target when it
// contains it; "harness.example" is the harness's own host.
type attrTestRules struct{ testRules }

func (attrTestRules) NamesTarget(_ string, input []byte, _, target string) bool {
	return strings.Contains(string(input), target)
}
func (attrTestRules) HarnessHost(host string) bool { return host == "harness.example" }

// spec 015 AC11: each effect carries its attribution, computed from the session's hook
// events; several lists its candidates; an effect with no window has none; an unassigned
// harness connection is harness.
func TestEffectsAPI_Attribution(t *testing.T) {
	e := newAPIEnv(t)
	r, err := store.OpenReader(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	e.h = Handler(Deps{Addr: addr, Reader: r, Rules: map[string]ClientRules{"client-a": attrTestRules{}},
		Now: func() time.Time { return baseTime.Add(100 * time.Second) }})
	id := store.SessionIdent{Principal: core.PrincipalLocal, Client: "client-a", Key: "s1"}
	seq := int64(0)
	hook := func(at int, phase core.HarnessPhase, tool, name, input string) {
		seq++
		ev := core.HarnessEvent{Phase: phase, SessionKey: id.Key, ToolCallID: tool, ToolName: name, WorkDir: "/w"}
		if input != "" {
			ev.ToolInput = []byte(input)
		}
		rec := &store.HookRecord{PrincipalID: id.Principal, Client: id.Client, Seq: seq,
			ReceivedAt: baseTime.Add(time.Duration(at) * time.Second), Payload: []byte(fmt.Sprintf(`{"n":%d}`, seq)), Event: ev}
		if err := e.s.SaveHook(t.Context(), rec); err != nil {
			t.Fatal(err)
		}
	}
	hook(1, core.PhasePreTool, "t1", "Write", `{"file_path":"/w/a.txt"}`)
	hook(2, core.PhasePreTool, "t2", "Bash", `{"command":"make"}`)
	hook(3, core.PhasePostTool, "t1", "Write", `{"file_path":"/w/a.txt"}`)
	hook(4, core.PhasePostTool, "t2", "Bash", `{"command":"make"}`)
	at := func(s int) time.Time { return baseTime.Add(time.Duration(s) * time.Second) }
	fx := func(path string, from, to time.Time) core.Effect {
		return core.Effect{Kind: core.EffectFileChange, At: to, Principal: id.Principal, Client: id.Client,
			SessionKey: id.Key, Root: "/w", Path: path, Change: core.FileCreated, Mode: "git", WindowFrom: from, WindowTo: to}
	}
	harness := core.Effect{Kind: core.EffectNetConn, At: at(5), EndedAt: at(6), Principal: id.Principal,
		Client: id.Client, Host: "harness.example", Port: 443, Via: "connect", Outcome: core.NetOK}
	effs := []core.Effect{fx("/w/a.txt", at(0), at(3)), fx("/w/out.bin", at(0), at(3)), fx("/w/old.txt", time.Time{}, time.Time{}), harness}
	if err := e.s.SaveEffects(t.Context(), effs, nil); err != nil {
		t.Fatal(err)
	}
	var page struct {
		Effects []effectJSON `json:"effects"`
	}
	get := func(path string) {
		t.Helper()
		rec := e.get(path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body)
		}
		page.Effects = nil
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
	}
	get("/api/sessions/" + encodeID(t, id.Principal, id.Client, id.Key) + "/effects")
	if len(page.Effects) != 3 {
		t.Fatalf("effects = %+v, want 3", page.Effects)
	}
	named, several, old := page.Effects[0].Attribution, page.Effects[1].Attribution, page.Effects[2].Attribution
	if named == nil || named.Label != "named" || named.Tool == nil || named.Tool.ID != "t1" || named.Tool.Name != "Write" {
		t.Errorf("a.txt attribution = %+v, want named t1 Write", named)
	}
	if several == nil || several.Label != "several" || several.Tool != nil || len(several.Candidates) != 2 ||
		several.Candidates[0].ID != "t1" || several.Candidates[1].ID != "t2" {
		t.Errorf("out.bin attribution = %+v, want several t1, t2", several)
	}
	if old != nil {
		t.Errorf("old.txt attribution = %+v, want none", old)
	}
	get("/api/effects/unassigned")
	if len(page.Effects) != 1 || page.Effects[0].Attribution == nil || page.Effects[0].Attribution.Label != "harness" {
		t.Errorf("unassigned = %+v, want one harness", page.Effects)
	}
}
