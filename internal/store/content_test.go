package store_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

const inlineMax = 4096

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// noise returns n bytes that zstd can't shrink to nothing, from a fixed seed.
func noise(n int, seed uint64) []byte {
	r := rand.New(rand.NewPCG(seed, seed))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.IntN(256))
	}
	return b
}

// body splits b into chunks, as a tee would have captured it.
func body(b []byte) core.Body {
	size := int64(len(b))
	var chunks [][]byte
	for len(b) > 0 {
		n := min(len(b), 1000)
		chunks = append(chunks, b[:n])
		b = b[n:]
	}
	return core.Body{Chunks: chunks, Size: size}
}

func exchange(id string, req, res []byte) *core.Exchange {
	start := time.Unix(1700000000, 123)
	return &core.Exchange{
		RequestID:      id,
		PrincipalID:    core.PrincipalLocal,
		Protocol:       "test",
		Client:         core.ClientUnknown,
		Auth:           core.AuthAPIKey,
		Method:         http.MethodPost,
		Path:           "/v1/things",
		Query:          "a=1&key=[REDACTED]",
		RequestHeader:  http.Header{"X-Test-Key": {"[REDACTED]"}, "Content-Type": {"application/json"}},
		ResponseHeader: http.Header{"Content-Type": {"text/event-stream"}},
		Status:         200,
		Start:          start,
		End:            start.Add(2 * time.Second),
		TTFB:           150 * time.Millisecond,
		HasTTFB:        true,
		Stream:         true,
		Request:        body(req),
		Response:       body(res),
	}
}

func blobPath(dir, hash string) string {
	return filepath.Join(dir, "blobs", hash[:2], hash[2:])
}

// blobFiles lists every regular file under <dir>/blobs.
func blobFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(filepath.Join(dir, "blobs"), func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !d.IsDir() {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

type contentRow struct {
	size     int64
	location string
	data     []byte
}

func contentRows(t *testing.T, db *sql.DB) map[string]contentRow {
	t.Helper()
	rows, err := db.Query("SELECT hash, size, location, data FROM content")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]contentRow{}
	for rows.Next() {
		var h string
		var r contentRow
		if err := rows.Scan(&h, &r.size, &r.location, &r.data); err != nil {
			t.Fatal(err)
		}
		out[h] = r
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func bodyHashes(t *testing.T, db *sql.DB, id string) (req, res sql.NullString) {
	t.Helper()
	err := db.QueryRow("SELECT request_body, response_body FROM exchanges WHERE request_id = ?", id).
		Scan(&req, &res)
	if err != nil {
		t.Fatalf("exchange %s: %v", id, err)
	}
	return req, res
}

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Sys().(*syscall.Stat_t).Ino
}

func unzstd(t *testing.T, b []byte) []byte {
	t.Helper()
	d, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	out, err := d.DecodeAll(b, nil)
	if err != nil {
		t.Fatalf("zstd decode: %v", err)
	}
	return out
}

func TestStore_BlobDedup(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	ctx := t.Context()
	large := noise(inlineMax+1000, 1)
	small := []byte(`{"model":"m","messages":[]}`)
	h := sum(large)

	if err := s.SaveExchange(ctx, exchange("r1", large, small)); err != nil {
		t.Fatalf("SaveExchange r1: %v", err)
	}
	before := inode(t, blobPath(dir, h))
	if err := s.SaveExchange(ctx, exchange("r2", large, small)); err != nil {
		t.Fatalf("SaveExchange r2: %v", err)
	}
	if after := inode(t, blobPath(dir, h)); after != before {
		t.Errorf("blob rewritten (inode %d → %d); an existing row must skip the write", before, after)
	}

	db := rawDB(t, dir)
	rows := contentRows(t, db)
	if len(rows) != 2 {
		t.Errorf("%d content rows, want 2 (one large, one small): %v", len(rows), rows)
	}
	if files := blobFiles(t, dir); len(files) != 1 {
		t.Errorf("blob files = %v, want exactly one", files)
	}
	for _, id := range []string{"r1", "r2"} {
		req, res := bodyHashes(t, db, id)
		if req.String != h {
			t.Errorf("%s request_body = %q, want %s", id, req.String, h)
		}
		if res.String != sum(small) {
			t.Errorf("%s response_body = %q, want %s", id, res.String, sum(small))
		}
	}
}

func TestStore_BlobBeforeRow(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	ctx := t.Context()
	large := noise(inlineMax*3, 2)
	h := sum(large)

	fault := errors.New("injected fault between blob and row")
	var sawBlob bool
	s.SetBeforeRowsHookForTest(func() error {
		_, err := os.Stat(blobPath(dir, h))
		sawBlob = err == nil
		return fault
	})
	err := s.SaveExchange(ctx, exchange("r1", large, nil))
	if !errors.Is(err, fault) {
		t.Fatalf("SaveExchange error = %v, want the injected fault", err)
	}
	if !sawBlob {
		t.Error("the blob was not on disk when the rows were about to be written")
	}

	db := rawDB(t, dir)
	var n int
	if err := db.QueryRow("SELECT count(*) FROM exchanges").Scan(&n); err != nil || n != 0 {
		t.Errorf("exchanges = %d (err %v), want 0 after the fault", n, err)
	}
	if rows := contentRows(t, db); len(rows) != 0 {
		t.Errorf("content rows = %v, want none after the fault", rows)
	}
	assertEveryBlobRowHasFile(t, dir, db)

	// The orphan is allowed, and a retry stores the row over it.
	s.SetBeforeRowsHookForTest(nil)
	if err := s.SaveExchange(ctx, exchange("r1", large, nil)); err != nil {
		t.Fatalf("SaveExchange after the fault: %v", err)
	}
	if rows := contentRows(t, db); rows[h].location != "blob" {
		t.Errorf("content row for %s = %+v, want a blob row", h, rows[h])
	}
	assertEveryBlobRowHasFile(t, dir, db)
}

func assertEveryBlobRowHasFile(t *testing.T, dir string, db *sql.DB) {
	t.Helper()
	for h, r := range contentRows(t, db) {
		if r.location != "blob" {
			continue
		}
		if _, err := os.Stat(blobPath(dir, h)); err != nil {
			t.Errorf("row %s points at a missing blob: %v", h, err)
		}
	}
}

func TestStore_SmallContentInline(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	b := noise(100, 3)
	if err := s.SaveExchange(t.Context(), exchange("r1", b, nil)); err != nil {
		t.Fatalf("SaveExchange: %v", err)
	}
	db := rawDB(t, dir)
	r, ok := contentRows(t, db)[sum(b)]
	if !ok {
		t.Fatal("no content row for the 100-byte body")
	}
	if r.location != "inline" || r.size != 100 || !bytes.Equal(r.data, b) {
		t.Errorf("row = {size %d, location %s, %d data bytes}, want inline, 100, the body",
			r.size, r.location, len(r.data))
	}
	if files := blobFiles(t, dir); len(files) != 0 {
		t.Errorf("blob files = %v, want none", files)
	}

	// Exactly 4096 bytes is still inline.
	edge := noise(inlineMax, 4)
	if err := s.SaveExchange(t.Context(), exchange("r2", edge, nil)); err != nil {
		t.Fatalf("SaveExchange: %v", err)
	}
	if r := contentRows(t, db)[sum(edge)]; r.location != "inline" {
		t.Errorf("a %d-byte body is %q, want inline", inlineMax, r.location)
	}
	if files := blobFiles(t, dir); len(files) != 0 {
		t.Errorf("blob files = %v, want none", files)
	}
}

func TestStore_LargeContentBlob(t *testing.T) {
	t.Run("identity", func(t *testing.T) {
		assertBlob(t, noise(inlineMax+1, 5))
	})
	// A gzip body is hashed and stored as captured, still compressed.
	t.Run("gzip_hashed_as_sent", func(t *testing.T) {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(noise(3*inlineMax, 6)); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if buf.Len() <= inlineMax {
			t.Fatalf("gzip body is %d bytes, want over %d", buf.Len(), inlineMax)
		}
		assertBlob(t, buf.Bytes())
	})
}

func assertBlob(t *testing.T, b []byte) {
	t.Helper()
	dir := t.TempDir()
	s, _ := open(t, dir)
	if err := s.SaveExchange(t.Context(), exchange("r1", b, nil)); err != nil {
		t.Fatalf("SaveExchange: %v", err)
	}
	h := sum(b)
	db := rawDB(t, dir)
	req, _ := bodyHashes(t, db, "r1")
	if req.String != h {
		t.Errorf("request_body = %q, want sha256 of the captured bytes %s", req.String, h)
	}
	r, ok := contentRows(t, db)[h]
	if !ok {
		t.Fatalf("no content row for %s", h)
	}
	if r.location != "blob" || r.data != nil || r.size != int64(len(b)) {
		t.Errorf("row = {size %d, location %s, data %d bytes}, want blob, %d, NULL",
			r.size, r.location, len(r.data), len(b))
	}
	files := blobFiles(t, dir)
	if len(files) != 1 || files[0] != blobPath(dir, h) {
		t.Fatalf("blob files = %v, want only %s", files, blobPath(dir, h))
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if got := unzstd(t, raw); !bytes.Equal(got, b) {
		t.Errorf("blob decompresses to %d bytes, not the %d-byte input", len(got), len(b))
	}
	for _, p := range []string{filepath.Join(dir, "blobs"), filepath.Dir(files[0]), files[0]} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0o600)
		if fi.IsDir() {
			want = 0o700
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s mode = %o, want %o", p, fi.Mode().Perm(), want)
		}
	}
}

func TestStore_OpenReaderDoesNotFailWrites(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	ctx := t.Context()
	if err := s.SaveExchange(ctx, exchange("r0", []byte("seed"), nil)); err != nil {
		t.Fatal(err)
	}

	u := "file:" + filepath.Join(dir, dbName) + "?mode=ro"
	reader, err := sql.Open("sqlite", u)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	tx, err := reader.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var seen int
	if err := tx.QueryRow("SELECT count(*) FROM exchanges").Scan(&seen); err != nil {
		t.Fatalf("reader: %v", err)
	}

	// The read transaction stays open across every write.
	for i := range 50 {
		req := noise(100+i*200, uint64(100+i))
		if err := s.SaveExchange(ctx, exchange(fmt.Sprintf("r%d", i+1), req, []byte("ok"))); err != nil {
			t.Fatalf("write %d under an open read transaction: %v", i, err)
		}
	}
	var still int
	if err := tx.QueryRow("SELECT count(*) FROM exchanges").Scan(&still); err != nil {
		t.Fatalf("reader: %v", err)
	}
	if still != seen {
		t.Errorf("reader's snapshot moved from %d to %d rows", seen, still)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := reader.QueryRow("SELECT count(*) FROM exchanges").Scan(&n); err != nil || n != 51 {
		t.Errorf("exchanges after the reader ended = %d (err %v), want 51", n, err)
	}
}

// Every Exchange field lands in its column.
func TestStore_SaveExchangeColumns(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	ex := exchange("r1", []byte("req"), []byte("res"))
	ex.RequestIncomplete = true
	ex.ClientDisconnected = true
	ex.GatewayError = "upstream_unreachable"
	ex.Request.Truncated = true
	if err := s.SaveExchange(t.Context(), ex); err != nil {
		t.Fatalf("SaveExchange: %v", err)
	}
	none := exchange("r2", nil, nil)
	none.HasTTFB = false
	none.RequestHeader = nil
	if err := s.SaveExchange(t.Context(), none); err != nil {
		t.Fatalf("SaveExchange with no bodies: %v", err)
	}

	db := rawDB(t, dir)
	var (
		principal, protocol, client, auth, method, path, query string
		reqH, resH                                             string
		status                                                 int
		started, ended                                         int64
		ttfb                                                   sql.NullInt64
		stream, reqTrunc, resTrunc, incomplete, truncated      bool
		disconnected, aborted                                  bool
		gwErr, reqBody, resBody, parse                         sql.NullString
	)
	err := db.QueryRow(`SELECT principal_id, protocol, client, auth, method, path, query,
		request_headers, response_headers, status, started_at, ttfb_ns, ended_at, stream,
		request_truncated, response_truncated, request_incomplete, truncated,
		client_disconnected, upstream_aborted, gateway_error, request_body, response_body, parse
		FROM exchanges WHERE request_id = 'r1'`).Scan(&principal, &protocol, &client, &auth,
		&method, &path, &query, &reqH, &resH, &status, &started, &ttfb, &ended, &stream,
		&reqTrunc, &resTrunc, &incomplete, &truncated, &disconnected, &aborted, &gwErr,
		&reqBody, &resBody, &parse)
	if err != nil {
		t.Fatal(err)
	}
	got := []any{principal, protocol, client, auth, method, path, query, status,
		started, ttfb.Int64, ended, stream, reqTrunc, resTrunc, incomplete, truncated,
		disconnected, aborted, gwErr.String, reqBody.String, resBody.String, parse.Valid}
	want := []any{"local", "test", "unknown", "api_key", "POST", "/v1/things",
		"a=1&key=[REDACTED]", 200, ex.Start.UnixNano(), int64(150 * time.Millisecond),
		ex.End.UnixNano(), true, true, false, true, true, true, false,
		"upstream_unreachable", sum([]byte("req")), sum([]byte("res")), false}
	for i := range want {
		if fmt.Sprint(got[i]) != fmt.Sprint(want[i]) {
			t.Errorf("column %d = %v, want %v", i, got[i], want[i])
		}
	}
	var h http.Header
	if err := json.Unmarshal([]byte(reqH), &h); err != nil || h.Get("X-Test-Key") != "[REDACTED]" ||
		h.Get("Content-Type") != "application/json" {
		t.Errorf("request_headers = %s (err %v)", reqH, err)
	}
	if !strings.Contains(resH, "text/event-stream") {
		t.Errorf("response_headers = %s", resH)
	}

	err = db.QueryRow(`SELECT request_headers, ttfb_ns, request_body, response_body, gateway_error
		FROM exchanges WHERE request_id = 'r2'`).Scan(&reqH, &ttfb, &reqBody, &resBody, &gwErr)
	if err != nil {
		t.Fatal(err)
	}
	if reqH != "{}" || ttfb.Valid || reqBody.Valid || resBody.Valid || gwErr.Valid {
		t.Errorf("r2 = headers %s, ttfb %v, bodies %v %v, gateway_error %v; want {} and NULLs",
			reqH, ttfb, reqBody, resBody, gwErr)
	}

	// A request ID is stored once.
	if err := s.SaveExchange(t.Context(), exchange("r1", nil, nil)); err == nil {
		t.Error("a second exchange with the same request ID was stored")
	}
}

func TestStore_SaveParse(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	ctx := t.Context()
	if err := s.SaveExchange(ctx, exchange("r1", []byte("req"), []byte("res"))); err != nil {
		t.Fatal(err)
	}
	small := []byte(`{"text":"hi"}`)
	large := []byte(`"` + strings.Repeat("x", inlineMax*2) + `"`)
	contents := []core.Content{{Hash: sum(small), Bytes: small}, {Hash: sum(large), Bytes: large}}
	events := []core.StoredEvent{
		{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"kind":"request"}`)},
		{Seq: 1, Kind: core.KindMessage, Source: core.SourceRequestHistory, ContentHash: sum(small),
			Payload: []byte(`{"kind":"message"}`)},
		{Seq: 2, Kind: core.KindToolCall, Source: core.SourceResponse, Partial: true,
			ContentHash: sum(large), ToolCallID: "call_1", Payload: []byte(`{"kind":"tool_call"}`)},
	}
	if err := s.SaveParse(ctx, "r1", core.PrincipalLocal, core.ParsePartial, events, contents); err != nil {
		t.Fatalf("SaveParse: %v", err)
	}

	db := rawDB(t, dir)
	var parse string
	if err := db.QueryRow("SELECT parse FROM exchanges WHERE request_id = 'r1'").Scan(&parse); err != nil ||
		parse != "partial" {
		t.Errorf("parse = %q (err %v), want partial", parse, err)
	}
	rows, err := db.Query(`SELECT seq, kind, schema_version, principal_id, source, partial,
		content_hash, tool_call_id, payload FROM events WHERE request_id = 'r1' ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var seq, sv int
		var kind, principal, payload string
		var partial bool
		var source, hash, call sql.NullString
		if err := rows.Scan(&seq, &kind, &sv, &principal, &source, &partial, &hash, &call, &payload); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%d %s v%d %s %v %v %v %v %s",
			seq, kind, sv, principal, source, partial, hash, call, payload))
	}
	want := []string{
		`0 request v1 local { false} false { false} { false} {"kind":"request"}`,
		fmt.Sprintf(`1 message v1 local {request_history true} false {%s true} { false} {"kind":"message"}`, sum(small)),
		fmt.Sprintf(`2 tool_call v1 local {response true} true {%s true} {call_1 true} {"kind":"tool_call"}`, sum(large)),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	cr := contentRows(t, db)
	if cr[sum(small)].location != "inline" || cr[sum(large)].location != "blob" {
		t.Errorf("content = %v, want the small inline and the large as a blob", cr)
	}
	assertEveryBlobRowHasFile(t, dir, db)

	// An unknown exchange is an error, and nothing is stored for it.
	err = s.SaveParse(ctx, "nope", core.PrincipalLocal, core.ParseOK, events[:1], nil)
	if err == nil || !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SaveParse for an unknown exchange = %v, want ErrNotFound", err)
	}
}
