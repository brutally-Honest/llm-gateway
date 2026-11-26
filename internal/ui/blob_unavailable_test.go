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
