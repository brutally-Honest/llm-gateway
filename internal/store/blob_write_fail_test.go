package store_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// readOnlyBlobs makes <dir>/blobs exist and unwritable, so writeBlob's mkdir of the
// hash's subdirectory fails. It returns a func that makes it writable again.
func readOnlyBlobs(t *testing.T, dir string) (restore func()) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	blobs := filepath.Join(dir, "blobs")
	if err := os.MkdirAll(blobs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blobs, 0o500); err != nil {
		t.Fatal(err)
	}
	restore = func() { _ = os.Chmod(blobs, 0o700) }
	t.Cleanup(restore)
	return restore
}

// A blob that cannot be written does not lose the call: the row is stored, its body
// reads as content unavailable; a later write of the same body heals it (spec 045 D5).
func TestBlobWriteFailure_ExchangeKeptAndHeals(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	restore := readOnlyBlobs(t, dir)

	body := noise(inlineMax+500, 7)
	h := sum(body)
	if err := s.SaveExchange(t.Context(), exchange("a", nil, body)); err != nil {
		t.Fatalf("SaveExchange with a failing blob dir: %v", err)
	}
	r := openReader(t, dir)
	row, err := r.Exchange("a")
	if err != nil {
		t.Fatalf("exchange row missing: %v", err)
	}
	if row.Status != 200 || row.Path != "/v1/things" || row.ResponseBody != h {
		t.Errorf("row = status %d path %q response_body %q, want 200 /v1/things %s",
			row.Status, row.Path, row.ResponseBody, h)
	}
	if _, err := r.Content(h); !errors.Is(err, store.ErrContentUnavailable) {
		t.Errorf("Content = %v, want ErrContentUnavailable", err)
	}
	if f := s.Failures(); f.BlobWrites != 1 || f.Writes != 0 {
		t.Errorf("Failures = %+v, want BlobWrites 1, Writes 0", f)
	}

	restore()
	if err := s.SaveExchange(t.Context(), exchange("b", nil, body)); err != nil {
		t.Fatal(err)
	}
	got, err := r.Content(h)
	if err != nil {
		t.Fatalf("Content after heal: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Error("healed content differs from the body")
	}
	if f := s.Failures(); f.BlobWrites != 1 || f.Writes != 0 {
		t.Errorf("Failures after heal = %+v, want BlobWrites 1, Writes 0", f)
	}
}

// SaveParse keeps its events when a content blob cannot be written.
func TestBlobWriteFailure_ParseKept(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	if err := s.SaveExchange(t.Context(), exchange("p", nil, nil)); err != nil {
		t.Fatal(err)
	}
	readOnlyBlobs(t, dir)

	data := noise(inlineMax+10, 9)
	h := sum(data)
	evs := []core.StoredEvent{{Seq: 0, Kind: core.KindMessage, ContentHash: h, Payload: []byte(`{}`)}}
	err := s.SaveParse(t.Context(), "p", core.PrincipalLocal, core.ParseOK, evs, []core.Content{{Hash: h, Bytes: data}})
	if err != nil {
		t.Fatalf("SaveParse: %v", err)
	}
	r := openReader(t, dir)
	got, err := r.Events("p")
	if err != nil || len(got) != 1 || got[0].ContentHash != h {
		t.Fatalf("events = %+v, err %v", got, err)
	}
	if _, err := r.Content(h); !errors.Is(err, store.ErrContentUnavailable) {
		t.Errorf("Content = %v, want ErrContentUnavailable", err)
	}
	if f := s.Failures(); f.BlobWrites != 1 || f.Writes != 0 {
		t.Errorf("Failures = %+v, want BlobWrites 1, Writes 0", f)
	}
}

// A call whose blob and transaction both fail is one lost call: it counts once, in
// Writes, and not also in BlobWrites.
func TestBlobWriteFailure_FailedWriteCountsOnce(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	readOnlyBlobs(t, dir)
	s.SetBeforeRowsHookForTest(func() error { return errors.New("disk full") })

	body := noise(inlineMax+500, 11)
	if err := s.SaveExchange(t.Context(), exchange("x", nil, body)); err == nil {
		t.Fatal("SaveExchange succeeded, want the hook's error")
	}
	if f := s.Failures(); f.BlobWrites != 0 || f.Writes != 1 {
		t.Errorf("Failures = %+v, want BlobWrites 0, Writes 1", f)
	}
}

// A failed data write counts once; a maintenance delete does not count.
func TestWriteFailures_Counted(t *testing.T) {
	s, _ := open(t, t.TempDir())
	err := s.SaveParse(t.Context(), "nope", core.PrincipalLocal, core.ParseOK, nil, nil)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SaveParse unknown id = %v, want ErrNotFound", err)
	}
	if f := s.Failures(); f.Writes != 1 || f.BlobWrites != 0 {
		t.Errorf("Failures = %+v, want Writes 1", f)
	}
	if err := s.SaveExchange(t.Context(), exchange("ok", nil, nil)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveEffects(t.Context(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSnapshot(t.Context(), store.SessionIdent{}); err != nil {
		t.Fatal(err)
	}
	if f := s.Failures(); f.Writes != 1 {
		t.Errorf("Writes after successes and a delete = %d, want 1", f.Writes)
	}
}

// Many concurrent writers all keep their rows, and every unwritable blob is counted.
func TestBlobWriteFailure_Concurrent(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	readOnlyBlobs(t, dir)

	const workers, each = 16, 20
	var wg sync.WaitGroup
	errs := make(chan error, workers*each)
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				body := noise(inlineMax+100, uint64(w*each+i+1)) // distinct, one blob each
				if err := s.SaveExchange(t.Context(), exchange(fmt.Sprintf("x-%d-%d", w, i), nil, body)); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("SaveExchange: %v", err)
	}
	if f := s.Failures(); f.BlobWrites != workers*each || f.Writes != 0 {
		t.Errorf("Failures = %+v, want BlobWrites %d, Writes 0", f, workers*each)
	}
	r := openReader(t, dir)
	for w := range workers {
		for i := range each {
			if _, err := r.Exchange(fmt.Sprintf("x-%d-%d", w, i)); err != nil {
				t.Fatalf("exchange x-%d-%d: %v", w, i, err)
			}
		}
	}
}
