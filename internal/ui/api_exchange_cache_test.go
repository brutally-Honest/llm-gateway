package ui

import (
	"context"
	"encoding/json"
	"net/http"
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

type detailJSON struct {
	ID       string `json:"id"`
	PrevID   string `json:"prev_id"`
	Carried  int    `json:"carried"`
	NewCount int    `json:"new_count"`
}

func getDetail(t *testing.T, base, id string, timeout time.Duration) (int, detailJSON) {
	t.Helper()
	cl := &http.Client{Timeout: timeout}
	resp, err := cl.Get(base + "/api/exchanges/" + id)
	if err != nil {
		t.Fatalf("GET /api/exchanges/%s: %v", id, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var d detailJSON
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
	if got != want {
		t.Fatalf("detail of b from the cached build = %+v, want %+v as an up-to-date build gives", got, want)
	}

	// c is newer than the cached build: its thread facts are not there, so it waits for
	// the build, and gets them once that lands.
	done := make(chan detailJSON, 1)
	go func() {
		code, d := getDetail(t, e.ts.URL, "c", 10*time.Second)
		if code != 200 {
			t.Errorf("detail of c = %d after the build landed", code)
		}
		done <- d
	}()
	select {
	case <-done:
		t.Fatal("detail of c answered before a build held it")
	case <-time.After(300 * time.Millisecond):
	}
	release()
	select {
	case d := <-done:
		if d.ID != "c" {
			t.Fatalf("detail of c = %+v", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("detail of c did not answer once the build landed")
	}
}
