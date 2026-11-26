package store_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

func openReader(t *testing.T, dir string) *store.Reader {
	t.Helper()
	r, err := store.OpenReader(dir)
	if err != nil {
		t.Fatalf("OpenReader(%s): %v", dir, err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// tree lists every path under dir, relative to it.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// seed stores two exchanges, the second with a blob-sized request body and a parse.
func seed(t *testing.T, s *store.Store) (small, large []byte, events []core.StoredEvent) {
	t.Helper()
	ctx := t.Context()
	small = []byte("small request")
	large = noise(inlineMax*3, 7)
	first := exchange("r1", small, []byte("res1"))
	if err := s.SaveExchange(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := exchange("r2", large, nil)
	second.Start = first.Start.Add(time.Second)
	second.HasTTFB = false
	second.GatewayError = "upstream_unreachable"
	if err := s.SaveExchange(ctx, second); err != nil {
		t.Fatal(err)
	}
	msg := []byte(`{"text":"hi"}`)
	// Given out of order: the reader orders by seq.
	events = []core.StoredEvent{
		{Seq: 10, Kind: core.KindUsage, Payload: []byte(`{"kind":"usage"}`)},
		{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"kind":"request"}`)},
		{Seq: 2, Kind: core.KindMessage, Source: core.SourceResponse, Partial: true,
			ContentHash: sum(msg), ToolCallID: "call_1", Payload: []byte(`{"kind":"message"}`)},
	}
	err := s.SaveParse(ctx, "r2", core.PrincipalLocal, core.ParsePartial, events,
		[]core.Content{{Hash: sum(msg), Bytes: msg}})
	if err != nil {
		t.Fatal(err)
	}
	return small, large, events
}

// A missing store is an error naming the path, and nothing is created: not the
// database, not the directory.
func TestReader_MissingStoreNotCreated(t *testing.T) {
	t.Run("empty_dir", func(t *testing.T) {
		dir := t.TempDir()
		r, err := store.OpenReader(dir)
		if err == nil {
			_ = r.Close()
			t.Fatal("OpenReader on an empty directory succeeded")
		}
		if !errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), filepath.Join(dir, dbName)) {
			t.Errorf("error = %v, want a not-exist error naming the database", err)
		}
		if got := tree(t, dir); len(got) != 0 {
			t.Errorf("directory holds %v, want nothing", got)
		}
	})
	t.Run("missing_dir", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "absent")
		r, err := store.OpenReader(dir)
		if err == nil {
			_ = r.Close()
			t.Fatal("OpenReader on a missing directory succeeded")
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("error = %v, want not-exist", err)
		}
		if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("directory was created (stat err %v)", err)
		}
	})
}

// A stopped gateway's store reads in full, and neither gateway.db nor a blob is
// written. SQLite may create -wal and -shm (research Q9).
func TestReader_StoppedStore(t *testing.T) {
	dir := t.TempDir()
	log, _ := newLogger()
	s, err := store.Open(dir, log)
	if err != nil {
		t.Fatal(err)
	}
	small, large, events := seed(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, dbName)
	before, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	filesBefore := tree(t, dir)

	r := openReader(t, dir)
	ex, err := r.Exchange("r1")
	if err != nil {
		t.Fatalf("Exchange(r1): %v", err)
	}
	want := exchange("r1", nil, nil)
	if ex.RequestID != "r1" || ex.PrincipalID != "local" || ex.Protocol != "test" ||
		ex.Client != "unknown" || ex.Auth != "api_key" || ex.Method != "POST" ||
		ex.Path != "/v1/things" || ex.Query != want.Query || ex.Status != 200 ||
		ex.StartedAt != want.Start.UnixNano() || ex.EndedAt != want.End.UnixNano() ||
		ex.TTFBNS == nil || *ex.TTFBNS != int64(150*time.Millisecond) || !ex.Stream ||
		ex.Parse != "" || ex.GatewayError != "" {
		t.Errorf("Exchange(r1) = %+v", ex)
	}
	if ex.RequestHeaders.Get("X-Test-Key") != "[REDACTED]" ||
		ex.ResponseHeaders.Get("Content-Type") != "text/event-stream" {
		t.Errorf("headers = %v / %v", ex.RequestHeaders, ex.ResponseHeaders)
	}
	if ex.RequestBody != sum(small) || ex.ResponseBody != sum([]byte("res1")) {
		t.Errorf("body hashes = %q, %q", ex.RequestBody, ex.ResponseBody)
	}

	last, err := r.Last()
	if err != nil {
		t.Fatalf("Last: %v", err)
	}
	if last.RequestID != "r2" || last.TTFBNS != nil || last.ResponseBody != "" ||
		last.GatewayError != "upstream_unreachable" || last.Parse != "partial" {
		t.Errorf("Last() = %+v, want r2 with no ttfb, no response body, parse partial", last)
	}

	for name, b := range map[string][]byte{"inline": small, "blob": large, "event": []byte(`{"text":"hi"}`)} {
		got, err := r.Content(sum(b))
		if err != nil {
			t.Errorf("Content(%s): %v", name, err)
		} else if !bytes.Equal(got, b) {
			t.Errorf("Content(%s) = %d bytes, want the %d stored", name, len(got), len(b))
		}
	}

	evs, err := r.Events("r2")
	if err != nil {
		t.Fatalf("Events(r2): %v", err)
	}
	var got []string
	for _, e := range evs {
		got = append(got, fmt.Sprintf("%s %d %s v%d %s %s %v %s %s %s", e.RequestID, e.Seq, e.Kind,
			e.SchemaVersion, e.PrincipalID, e.Source, e.Partial, e.ContentHash, e.ToolCallID, e.Payload))
	}
	wantEvents := []string{
		`r2 0 request v1 local  false   {"kind":"request"}`,
		fmt.Sprintf(`r2 2 message v1 local response true %s call_1 {"kind":"message"}`, events[2].ContentHash),
		`r2 10 usage v1 local  false   {"kind":"usage"}`,
	}
	if strings.Join(got, "\n") != strings.Join(wantEvents, "\n") {
		t.Errorf("Events(r2):\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantEvents, "\n"))
	}
	if evs, err := r.Events("r1"); err != nil || len(evs) != 0 {
		t.Errorf("Events(r1) = %v, %v; want none", evs, err)
	}

	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	after, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Errorf("gateway.db changed: mtime %v -> %v, size %d -> %d",
			before.ModTime(), after.ModTime(), before.Size(), after.Size())
	}
	allowed := map[string]bool{dbName + "-wal": true, dbName + "-shm": true}
	was := map[string]bool{}
	for _, p := range filesBefore {
		was[p] = true
	}
	for _, p := range tree(t, dir) {
		if !was[p] && !allowed[p] {
			t.Errorf("the reader created %s", p)
		}
	}
}

// A reader beside a running writer sees committed writes and never holds it up.
func TestReader_WhileWriterOpen(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	ctx := t.Context()
	if err := s.SaveExchange(ctx, exchange("r0", []byte("seed"), nil)); err != nil {
		t.Fatal(err)
	}
	r := openReader(t, dir)
	if _, err := r.Exchange("r0"); err != nil {
		t.Fatalf("Exchange(r0): %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var readErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := r.Last(); err != nil {
				readErr = err
				return
			}
			if _, err := r.Events("r0"); err != nil {
				readErr = err
				return
			}
		}
	}()
	for i := 1; i <= 30; i++ {
		wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		ex := exchange(fmt.Sprintf("r%d", i), noise(100+i*300, uint64(i)), []byte("ok"))
		ex.Start = ex.Start.Add(time.Duration(i) * time.Second)
		err := s.SaveExchange(wctx, ex)
		cancel()
		if err != nil {
			t.Fatalf("write %d beside a reader: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()
	if readErr != nil {
		t.Fatalf("reader: %v", readErr)
	}

	last, err := r.Last()
	if err != nil || last.RequestID != "r30" {
		t.Errorf("Last() = %q, %v; want r30", last.RequestID, err)
	}
	ex, err := r.Exchange("r17")
	if err != nil {
		t.Fatalf("Exchange(r17): %v", err)
	}
	b, err := r.Content(ex.RequestBody)
	if err != nil || !bytes.Equal(b, noise(100+17*300, 17)) {
		t.Errorf("Content(r17 request) = %d bytes, %v", len(b), err)
	}
}

func TestReader_UnknownID(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	r := openReader(t, dir)

	if _, err := r.Last(); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Last() on an empty store = %v, want ErrNotFound", err)
	}
	if err := s.SaveExchange(t.Context(), exchange("r1", []byte("x"), nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Exchange("nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Exchange(nope) = %v, want ErrNotFound", err)
	}
	if _, err := r.Events("nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Events(nope) = %v, want ErrNotFound", err)
	}
	for _, h := range []string{sum([]byte("never stored")), "../../gateway.db", ""} {
		if _, err := r.Content(h); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Content(%q) = %v, want ErrNotFound", h, err)
		}
	}
}

// Only a permanent blob failure is ErrContentUnavailable: a blob that is a symlink loop
// (ELOOP, a failure that may pass) is a plain error; a permission-denied blob, a directory
// at the blob path (EISDIR) and a file where the blobs/xx directory belongs (ENOTDIR) are
// unavailable.
func TestReader_TransientBlobErrorIsNotUnavailable(t *testing.T) {
	dir := t.TempDir()
	log, _ := newLogger()
	s, err := store.Open(dir, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	loop, noPerm := noise(inlineMax*2, 21), noise(inlineMax*2, 22)
	asDir, badLayout := noise(inlineMax*2, 23), noise(inlineMax*2, 24)
	if err := s.SaveExchange(t.Context(), exchange("r1", loop, noPerm)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveExchange(t.Context(), exchange("r2", asDir, badLayout)); err != nil {
		t.Fatal(err)
	}
	r := openReader(t, dir)
	blob := func(b []byte) string {
		h := sum(b)
		return filepath.Join(dir, "blobs", h[:2], h[2:])
	}
	if err := os.Remove(blob(loop)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(blob(loop), blob(loop)); err != nil {
		t.Fatal(err)
	}
	_, err = r.Content(sum(loop))
	if err == nil || errors.Is(err, store.ErrContentUnavailable) || errors.Is(err, store.ErrNotFound) {
		t.Errorf("symlink-loop blob: err = %v, want a plain error", err)
	}
	if err := os.Remove(blob(asDir)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(blob(asDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Content(sum(asDir)); !errors.Is(err, store.ErrContentUnavailable) {
		t.Errorf("directory at the blob path: err = %v, want ErrContentUnavailable", err)
	}
	badDir := filepath.Dir(blob(badLayout))
	if err := os.Rename(badDir, badDir+".away"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Content(sum(badLayout)); !errors.Is(err, store.ErrContentUnavailable) {
		t.Errorf("file where the blob directory belongs: err = %v, want ErrContentUnavailable", err)
	}
	if os.Geteuid() == 0 {
		return
	}
	if err := os.Chmod(blob(noPerm), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blob(noPerm), 0o600) })
	if _, err := r.Content(sum(noPerm)); !errors.Is(err, store.ErrContentUnavailable) {
		t.Errorf("chmod 000 blob: err = %v, want ErrContentUnavailable", err)
	}
}

// A content row whose blob cannot be read is ErrContentUnavailable; a hash with no row is
// ErrNotFound and never ErrContentUnavailable.
func TestReader_ContentUnavailableBlob(t *testing.T) {
	dir := t.TempDir()
	log, _ := newLogger()
	s, err := store.Open(dir, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	gone, bad := noise(inlineMax*2, 11), noise(inlineMax*2, 12)
	if err := s.SaveExchange(t.Context(), exchange("r1", gone, bad)); err != nil {
		t.Fatal(err)
	}
	r := openReader(t, dir)
	blob := func(b []byte) string {
		h := sum(b)
		return filepath.Join(dir, "blobs", h[:2], h[2:])
	}
	if _, err := r.Content(sum(gone)); err != nil {
		t.Fatalf("undamaged: %v", err)
	}
	if err := os.Remove(blob(gone)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blob(bad), []byte("not zstd at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{"deleted": gone, "corrupt": bad} {
		_, err := r.Content(sum(b))
		if !errors.Is(err, store.ErrContentUnavailable) || errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrContentUnavailable only", name, err)
		}
		if err != nil && !strings.Contains(err.Error(), blob(b)) {
			t.Errorf("%s: error %q does not name the blob path", name, err)
		}
	}
	_, err = r.Content(sum([]byte("never stored")))
	if !errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrContentUnavailable) {
		t.Errorf("unknown hash: err = %v, want ErrNotFound only", err)
	}
}
