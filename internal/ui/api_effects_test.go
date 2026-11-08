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
