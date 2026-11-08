package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// slowBuilds models a session whose writer commits faster than a timeline build runs, as a
// live session of 2,000 calls does (013 manual check, F5). The store's version counts the
// committed exchanges e1..eN; a build started at version v lands at v after buildTime, and
// a read answers only with exchanges the last landed build holds.
type slowBuilds struct {
	mu        sync.Mutex
	store     int // e1..e<store> are committed
	built     int // the last landed build holds e1..e<built>
	buildTime time.Duration
}

func versionAt(n int) store.Version {
	return store.Version{ExCount: n, ExMaxRow: int64(n), Parsed: n}
}

func (f *slowBuilds) write() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.store++
	return fmt.Sprintf("e%d", f.store)
}

func (f *slowBuilds) builtNow() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.built
}

func (f *slowBuilds) read(_ context.Context, who sessIdent, ids []string, summary, entries bool) (liveMsg, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var m liveMsg
	if summary {
		m.Summary = &sessionSummary{ID: who.id(), ExchangeCount: f.store}
	}
	if entries {
		m.ReadAt, m.BuiltAt = versionAt(f.store), versionAt(f.built)
		m.StaleEntries = f.built < f.store
		m.Position = positionOf(m.BuiltAt)
		for _, id := range ids {
			if n, _ := strconv.Atoi(strings.TrimPrefix(id, "e")); n <= f.built {
				m.Exchanges = append(m.Exchanges, liveExchange{RowID: int64(n), RequestID: id,
					Entries: []entryJSON{{ID: id + ":0", ExchangeID: id, Kind: kindExchange}}})
			}
		}
	}
	return m, nil
}

// fresh is a build: it reaches the store's version as it starts, and lands buildTime later.
func (f *slowBuilds) fresh(ctx context.Context, _ sessIdent, _ bool) error {
	f.mu.Lock()
	target := f.store
	f.mu.Unlock()
	select {
	case <-time.After(f.buildTime):
	case <-ctx.Done():
		return ctx.Err()
	}
	f.mu.Lock()
	f.built = max(f.built, target)
	f.mu.Unlock()
	return nil
}

// While commits keep arriving faster than a build, a session stream still gets every
// exchange a landed build covers once that build lands, not only after the writes stop;
// and it never gets an exchange before a build holds it.
func TestHub_SteadyWritesStillDeliverWhatEachLandedBuildCovers(t *testing.T) {
	f := &slowBuilds{buildTime: 60 * time.Millisecond}
	h := newHub(Deps{}, hubQueueSize)
	h.read, h.fresh = f.read, f.fresh
	defer h.Close()
	sub, err := h.subscribe(testSess.id())
	if err != nil {
		t.Fatal(err)
	}
	h.start()

	stop := make(chan struct{})
	written := make(chan int)
	go func() {
		n := 0
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				written <- n
				return
			case <-tick.C:
				h.Publish(commit(f.write()))
				n++
			}
		}
	}()

	got := map[int]bool{}
	early := time.After(1500 * time.Millisecond)
	var builtAtStop int
collect:
	for {
		select {
		case m := <-sub.ch:
			built := f.builtNow()
			for _, x := range m.Exchanges {
				n, _ := strconv.Atoi(strings.TrimPrefix(x.RequestID, "e"))
				if n > built {
					t.Fatalf("%s was sent before a build held it (builds reach e%d)", x.RequestID, built)
				}
				got[n] = true
			}
		case <-early:
			builtAtStop = f.builtNow()
			close(stop)
			break collect
		}
	}
	total := <-written
	if total < 50 || builtAtStop < 20 {
		t.Fatalf("too little traffic to judge: %d commits written, builds reached e%d", total, builtAtStop)
	}
	// Every exchange the builds held well before the writer stopped has arrived: a build
	// takes 60 ms, so allow the last few builds' worth of commits to be still in flight.
	covered := builtAtStop - 3*int(f.buildTime/(10*time.Millisecond))
	for n := 1; n <= covered; n++ {
		if !got[n] {
			t.Fatalf("e%d was covered by a landed build but not delivered while writes continued (got %d of %d covered; %d written)",
				n, len(got), covered, total)
		}
	}
}
