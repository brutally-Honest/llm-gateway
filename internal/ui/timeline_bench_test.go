package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// benchCalls is the session size of the 013 load check (CR-003, Q1).
const benchCalls = 2000

// seedBenchSession stores benchCalls exchanges of one session, shaped like the seed script:
// a new thread every 100 calls, a prompt every 10 calls and an error on every 50th. Each call
// carries its thread's history, as a client does, so the request bodies grow within a thread.
func seedBenchSession(b *testing.B, s *store.Store) {
	b.Helper()
	var history []string
	for i := range benchCalls {
		n := i + 1
		if i%100 == 0 {
			history = nil
		}
		if i%10 == 0 {
			history = append(history, fmt.Sprintf("user:Seeded prompt before call %d.", n))
		}
		ex := &core.Exchange{RequestID: fmt.Sprintf("bench-%04d", n), PrincipalID: core.PrincipalLocal,
			Protocol: "test", Client: "cc", Auth: core.AuthAPIKey, Method: "POST", Path: "/v1/x", Status: 200,
			SessionKey: "k", Start: baseTime.Add(time.Duration(n) * time.Second)}
		ex.End = ex.Start.Add(time.Second)
		if err := s.SaveExchange(b.Context(), ex); err != nil {
			b.Fatal(err)
		}
		evs := []core.StoredEvent{{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"model":"m1"}`)}}
		var contents []core.Content
		for _, m := range history {
			role, txt, _ := strings.Cut(m, ":")
			raw, _ := json.Marshal(map[string]string{"type": "text", "text": txt})
			bh, mh := sha("b-"+m), sha("m-"+m)
			contents = append(contents, core.Content{Hash: bh, Bytes: raw}, core.Content{Hash: mh, Bytes: []byte(mh)})
			p, _ := json.Marshal(map[string]any{"role": role, "blocks": []map[string]string{{"hash": bh, "type": "text"}}})
			evs = append(evs, core.StoredEvent{Seq: len(evs), Kind: core.KindMessage, ContentHash: mh, Payload: p})
		}
		if n%50 == 0 {
			evs = append(evs, core.StoredEvent{Seq: len(evs), Kind: core.KindError,
				Payload: []byte(`{"status":529,"type":"overloaded_error","message":"seeded error"}`)})
		}
		evs = append(evs, core.StoredEvent{Seq: len(evs), Kind: core.KindUsage,
			Payload: []byte(fmt.Sprintf(`{"input_tokens":%d,"output_tokens":20}`, 100+(n%100)*40))})
		if err := s.SaveParse(b.Context(), ex.RequestID, core.PrincipalLocal, core.ParseOK, evs, contents); err != nil {
			b.Fatal(err)
		}
		history = append(history, fmt.Sprintf("assistant:Reply to call %d.", n))
	}
}

// BenchmarkTimeline_2000Calls measures one `limit=2000` timeline request over a 2,000-call
// session: cold, which builds the whole session in a fresh cache, and cached, which slices a
// build already in the cache. It is a measurement, not a behaviour test (research.md Notes).
func BenchmarkTimeline_2000Calls(b *testing.B) {
	dir := b.TempDir()
	s, err := store.Open(dir, zap.NewNop())
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	seedBenchSession(b, s)
	r, err := store.OpenReader(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	deps := func(c *TimelineCache) Deps {
		return Deps{Addr: addr, Reader: r, Rules: map[string]ClientRules{"cc": testRules{}}, Timelines: c,
			Now: func() time.Time { return baseTime.Add(100 * time.Second) }}
	}
	path := "/api/sessions/" + sessIdent{core.PrincipalLocal, "cc", "k"}.id() + "/timeline?limit=2000"
	get := func(h http.Handler) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "http://"+addr+path, nil))
		if rec.Code != http.StatusOK {
			b.Fatalf("timeline = %d %s", rec.Code, rec.Body.String())
		}
	}

	b.Run("cold", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			get(Handler(deps(NewTimelineCache(context.Background(), nil))))
		}
	})
	b.Run("cached", func(b *testing.B) {
		h := Handler(deps(NewTimelineCache(context.Background(), nil)))
		get(h) // the first request builds; the timed ones slice its build
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			get(h)
		}
	})
}

// benchSessions and benchSessionCalls are the store of the 017 sessions-list budget
// (spec 017 Constraints): 500 sessions of 20 calls, a prompt every 5 calls.
const (
	benchSessions     = 500
	benchSessionCalls = 20
)

// seedBenchSessions stores benchSessions sessions of benchSessionCalls calls each, one second
// apart, every call carrying its prompt and a reply, with usage.
func seedBenchSessions(b *testing.B, s *store.Store) {
	b.Helper()
	for si := range benchSessions {
		key := fmt.Sprintf("s%03d", si)
		for i := range benchSessionCalls {
			n := si*benchSessionCalls + i + 1
			ex := &core.Exchange{RequestID: fmt.Sprintf("%s-%02d", key, i), PrincipalID: core.PrincipalLocal,
				Protocol: "test", Client: "cc", Auth: core.AuthAPIKey, Method: "POST", Path: "/v1/x", Status: 200,
				SessionKey: key, Start: baseTime.Add(time.Duration(n) * time.Second)}
			ex.End = ex.Start.Add(time.Second)
			if err := s.SaveExchange(b.Context(), ex); err != nil {
				b.Fatal(err)
			}
			evs := []core.StoredEvent{{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"model":"m1"}`)}}
			var contents []core.Content
			for _, m := range []string{fmt.Sprintf("user:Prompt %d of %s.", i/5, key), fmt.Sprintf("assistant:Reply %d.", i)} {
				role, txt, _ := strings.Cut(m, ":")
				raw, _ := json.Marshal(map[string]string{"type": "text", "text": txt})
				bh, mh := sha("b-"+m), sha("m-"+m)
				contents = append(contents, core.Content{Hash: bh, Bytes: raw}, core.Content{Hash: mh, Bytes: []byte(mh)})
				p, _ := json.Marshal(map[string]any{"role": role, "blocks": []map[string]string{{"hash": bh, "type": "text"}}})
				evs = append(evs, core.StoredEvent{Seq: len(evs), Kind: core.KindMessage, ContentHash: mh, Payload: p})
			}
			evs = append(evs, core.StoredEvent{Seq: len(evs), Kind: core.KindUsage,
				Payload: []byte(`{"input_tokens":300,"output_tokens":20,"cache_read_input_tokens":1000}`)})
			if err := s.SaveParse(b.Context(), ex.RequestID, core.PrincipalLocal, core.ParseOK, evs, contents); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkSessionsList500 measures the sessions list's first page (50 rows) and today's
// totals over a 500-session store: cold, with a fresh digest cache so every row is
// computed, and cached. It is the before/after measurement of spec 017's Constraints
// (research.md), not a behaviour test.
func BenchmarkSessionsList500(b *testing.B) {
	dir := b.TempDir()
	s, err := store.Open(dir, zap.NewNop())
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	seedBenchSessions(b, s)
	r, err := store.OpenReader(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	deps := func() Deps {
		return Deps{Addr: addr, Reader: r, Rules: map[string]ClientRules{"cc": testRules{}},
			Digests: newDigestCache(1000), Now: func() time.Time { return baseTime.Add(time.Hour) }}
	}
	get := func(h http.Handler, path string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "http://"+addr+path, nil))
		if rec.Code != http.StatusOK {
			b.Fatalf("%s = %d %s", path, rec.Code, rec.Body.String())
		}
	}
	const list = "/api/sessions?limit=50"
	b.Run("list-cold", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			get(Handler(deps()), list)
		}
	})
	b.Run("list-cached", func(b *testing.B) {
		h := Handler(deps())
		get(h, list)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			get(h, list)
		}
	})
	b.Run("today", func(b *testing.B) {
		h := Handler(deps())
		b.ReportAllocs()
		for range b.N {
			get(h, "/api/today?since="+baseTime.UTC().Format(time.RFC3339))
		}
	})
}
