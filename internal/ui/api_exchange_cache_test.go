package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"
)

// gateBuilds makes every session build wait for release (or its context) before running
// the real one, as a 2,000-call build under live traffic does; restore puts it back.
func gateBuilds(t *testing.T) (release func()) {
	t.Helper()
	gate := make(chan struct{})
	real := buildSessionFn
	buildSessionFn = func(c sessionSource, who sessIdent, rules ClientRules, cut int) (builtSession, error) {
		if ctx, ok := c.(interface{ Context() context.Context }); ok {
			select {
			case <-gate:
			case <-ctx.Context().Done():
				return builtSession{}, ctx.Context().Err()
			}
		} else {
			<-gate
		}
		return real(c, who, rules, cut)
	}
	released := false
	t.Cleanup(func() {
		if !released {
			close(gate)
		}
		buildSessionFn = real
	})
	return func() { released = true; close(gate) }
}

func getDetail(t *testing.T, base, id string, timeout time.Duration) (int, map[string]any) {
	t.Helper()
	cl := &http.Client{Timeout: timeout}
	resp, err := cl.Get(base + "/api/exchanges/" + id)
	if err != nil {
		t.Fatalf("GET /api/exchanges/%s: %v", id, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var d map[string]any
	if resp.StatusCode == 200 {
		if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode, d
}

// A call's detail is served from the session's cached build, as the timeline, digest and
// snapshot are, so a slow rebuild under live traffic does not hold it past the store
// timeout (013 manual check, F9). Only a call newer than the cached build waits.
func TestAPI_ExchangeDetailAnswersFromCachedBuildWithoutWaiting(t *testing.T) {
	e := newStreamEnv(t, nil)
	e.commitA()
	e.saveToolExchange("b", 20)
	resp, err := http.Get(e.ts.URL + "/api/sessions/" + sid() + "/timeline") // caches a and b
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	settleTimelines(t, e.hub.Timelines())
	_, want := getDetail(t, e.ts.URL, "b", 5*time.Second)

	e.saveToolExchange("c", 30) // the cached build is now older than the store
	release := gateBuilds(t)

	start := time.Now()
	code, got := getDetail(t, e.ts.URL, "b", 2*time.Second)
	if code != 200 {
		t.Fatalf("detail of b = %d while a rebuild is held, want 200 from the cached build", code)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("detail of b took %v while a rebuild is held, want an answer from the cached build at once", took)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detail of b from the cached build = %v, want %v as an up-to-date build gives", got, want)
	}

	// c is newer than the cached build. Its detail no longer waits for one (S1): the title
	// and #n come only from cached views, so while the rebuild is held it answers at once,
	// without a #n, and a build that lands later changes nothing about that answer.
	start = time.Now()
	code, d := getDetail(t, e.ts.URL, "c", 2*time.Second)
	if code != 200 || d["id"] != "c" {
		t.Fatalf("detail of c = %d %v while a rebuild is held, want 200", code, d)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("detail of c took %v while a rebuild is held, want an answer at once", took)
	}
	if _, ok := d["call_n"]; ok {
		t.Fatalf("detail of c has call_n %v from a view older than c", d["call_n"])
	}
	release()
}
