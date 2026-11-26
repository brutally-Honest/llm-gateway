package ui

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

func blobPath(dir, hash string) string {
	return filepath.Join(dir, "blobs", hash[:2], hash[2:])
}

func sessIDFor(key string) string {
	return sessionID(store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: key})
}

func bigText(tag string) string {
	return tag + ":" + strings.Repeat(tag+"-payload ", 600) // well over store.InlineMax
}

// A blob that is gone or unreadable is a placeholder for its block, never an error for the
// session list, the session's view, or the other sessions (spec 045 AC1, AC2).
func TestBlobUnavailable_PerBlockPlaceholder(t *testing.T) {
	e := newAPIEnv(t)
	logs := func() *observer.ObservedLogs {
		c, l := observer.New(zap.WarnLevel)
		r, err := store.OpenReader(e.dir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.Close() })
		e.h = Handler(Deps{Addr: addr, Reader: r, Log: zap.New(c),
			Now: func() time.Time { return baseTime.Add(100 * time.Second) }})
		return l
	}()

	for i, k := range []string{"a", "b", "c"} {
		id := "e" + k
		ex := &core.Exchange{
			RequestID: id, PrincipalID: core.PrincipalLocal, Protocol: "test", Client: "cc",
			Auth: core.AuthAPIKey, Method: "POST", Path: "/v1/x", Status: 200, SessionKey: k,
			Start: baseTime.Add(time.Duration(i+1) * time.Second),
		}
		ex.End = ex.Start.Add(time.Second)
		if k == "a" {
			body := []byte(bigText("reqbody"))
			ex.Request = core.Body{Chunks: [][]byte{body}, Size: int64(len(body))}
		}
		if err := e.s.SaveExchange(t.Context(), ex); err != nil {
			t.Fatal(err)
		}
		e.parseMsgs(id, "ok", []any{"user", text(bigText("text-" + k))}, []any{"assistant", text("short " + k)})
	}
	blockHash := func(k string) string { return sha(fmt.Sprintf("e%s-0-0", k)) }
	tlURL := func(k string) string { return "/api/sessions/" + sessIDFor(k) + "/timeline" }
	entriesText := func(k string) string {
		t.Helper()
		return e.get(tlURL(k)).Body.String()
	}

	// C is built before the damage and must read the same after it.
	beforeC := entriesText("c")
	if strings.Contains(beforeC, timeline.Unavailable) {
		t.Fatal("undamaged session already shows the placeholder")
	}
	for _, p := range []string{blobPath(e.dir, blockHash("a")), blobPath(e.dir, blockHash("b"))} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("seed has no blob at %s: %v", p, err)
		}
	}

	if err := os.Remove(blobPath(e.dir, blockHash("a"))); err != nil {
		t.Fatal(err)
	}
	bDamaged := os.Geteuid() != 0
	if bDamaged {
		p := blobPath(e.dir, blockHash("b"))
		if err := os.Chmod(p, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(p, 0o600) })
	}
	bodyHash := sha(bigText("reqbody"))
	if err := os.Remove(blobPath(e.dir, bodyHash)); err != nil {
		t.Fatal(err)
	}

	p := e.page("/api/sessions")
	if len(p.Items) != 3 {
		t.Fatalf("sessions listed = %d, want 3: %v", len(p.Items), keys(p))
	}

	if got := entriesText("a"); !strings.Contains(got, timeline.Unavailable) {
		t.Errorf("session A timeline lacks %q: %s", timeline.Unavailable, got)
	}
	if bDamaged {
		if got := entriesText("b"); !strings.Contains(got, timeline.Unavailable) {
			t.Errorf("session B timeline lacks %q: %s", timeline.Unavailable, got)
		}
	}
	if afterC := entriesText("c"); afterC != beforeC {
		t.Errorf("session C changed:\nbefore %s\nafter  %s", beforeC, afterC)
	}

	rec := e.get("/api/exchanges/ea/body?part=request")
	wantEnvelope(t, rec, http.StatusNotFound, "body_not_stored")
	var lines int
	for _, en := range logs.FilterMessage("body unavailable").All() {
		if strings.Contains(fmt.Sprint(en.ContextMap()), bodyHash) {
			lines++
		}
	}
	if lines != 1 || logs.Len() != 1 {
		t.Errorf("log lines naming the hash = %d, all warn lines = %d, want 1 and 1", lines, logs.Len())
	}
	if strings.Contains(fmt.Sprint(logs.All()[0].ContextMap()), "reqbody-payload") {
		t.Error("the log line carries the body")
	}
}

// A call whose blobs could not be written is still stored and listed: its body and its
// big blocks read as unavailable, never as an error (spec 045 D5).
func TestBlobWriteFailure_CallListedWithoutBody(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	e := newAPIEnv(t)
	blobs := filepath.Join(e.dir, "blobs")
	if err := os.MkdirAll(blobs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blobs, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blobs, 0o700) })

	body := []byte(bigText("reqbody"))
	ex := &core.Exchange{
		RequestID: "ew", PrincipalID: core.PrincipalLocal, Protocol: "test", Client: "cc",
		Auth: core.AuthAPIKey, Method: "POST", Path: "/v1/x", Status: 200, SessionKey: "w",
		Start:   baseTime.Add(time.Second),
		Request: core.Body{Chunks: [][]byte{body}, Size: int64(len(body))},
	}
	ex.End = ex.Start.Add(time.Second)
	if err := e.s.SaveExchange(t.Context(), ex); err != nil {
		t.Fatalf("SaveExchange: %v", err)
	}
	e.parseMsgs("ew", "ok", []any{"user", text(bigText("text-w"))}, []any{"assistant", text("short w")})
	if f := e.s.Failures(); f.BlobWrites == 0 || f.Writes != 0 {
		t.Fatalf("Failures = %+v, want blob failures and no write failures", f)
	}

	p := e.page("/api/sessions")
	if len(p.Items) != 1 {
		t.Fatalf("sessions listed = %d, want 1: %v", len(p.Items), keys(p))
	}
	if got := e.get("/api/sessions/" + sessIDFor("w") + "/timeline"); got.Code != http.StatusOK ||
		!strings.Contains(got.Body.String(), timeline.Unavailable) {
		t.Errorf("timeline = %d, want 200 with %q: %s", got.Code, timeline.Unavailable, got.Body.String())
	}
	wantEnvelope(t, e.get("/api/exchanges/ew/body?part=request"), http.StatusNotFound, "body_not_stored")
}

// A text block whose blob path is a directory (EISDIR: the on-disk layout is wrong, a retry
// never helps) is a placeholder for the block, not a build error: the session is listed and
// not pending, and its timeline shows the placeholder (spec 045 AC1, D20).
func TestBlobUnavailable_DirectoryAtBlobPath(t *testing.T) {
	e := newAPIEnv(t)
	ex := &core.Exchange{
		RequestID: "ed", PrincipalID: core.PrincipalLocal, Protocol: "test", Client: "cc",
		Auth: core.AuthAPIKey, Method: "POST", Path: "/v1/x", Status: 200, SessionKey: "d",
		Start: baseTime.Add(time.Second),
	}
	ex.End = ex.Start.Add(time.Second)
	if err := e.s.SaveExchange(t.Context(), ex); err != nil {
		t.Fatal(err)
	}
	e.parseMsgs("ed", "ok", []any{"user", text(bigText("text-d"))}, []any{"assistant", text("short d")})
	p := blobPath(e.dir, sha("ed-0-0"))
	if err := os.Remove(p); err != nil {
		t.Fatalf("seed has no blob at %s: %v", p, err)
	}
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}

	list := e.page("/api/sessions")
	if len(list.Items) != 1 || isPending(list.Items[0]) {
		t.Fatalf("sessions = %v, want one row that is not pending", list.Items)
	}
	rec := e.get("/api/sessions/" + sessIDFor("d") + "/timeline")
	if rec.Code != http.StatusOK {
		t.Fatalf("timeline status = %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), timeline.Unavailable) {
		t.Errorf("timeline lacks %q: %s", timeline.Unavailable, rec.Body)
	}
}
