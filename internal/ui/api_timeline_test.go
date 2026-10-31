package ui

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

type tlPage struct {
	Entries     []map[string]any `json:"entries"`
	NextAfter   *string          `json:"next_after"`
	StreamSince int64            `json:"stream_since"`
}

func (e *apiEnv) timeline(path string) tlPage {
	e.t.Helper()
	rec := e.get(path)
	if rec.Code != 200 {
		e.t.Fatalf("%s = %d %s", path, rec.Code, rec.Body)
	}
	var p tlPage
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		e.t.Fatal(err)
	}
	return p
}

// msg is one stored message block set to parse: role, kind of block and its content.
type blk struct{ typ, content string }

// parseMsgs stores events for id: a request event, then one message per blocks set.
func (e *apiEnv) parseMsgs(id, parse string, msgs ...[]any) {
	e.t.Helper()
	evs := []core.StoredEvent{{Seq: 0, Kind: core.KindRequest, Payload: []byte(`{"model":"m1"}`)}}
	var contents []core.Content
	for i, m := range msgs {
		role := m[0].(string)
		var blocks []map[string]string
		var key strings.Builder
		key.WriteString(role)
		for j, b := range m[1:] {
			bl := b.(blk)
			h := sha(fmt.Sprintf("%s-%d-%d", id, i, j))
			contents = append(contents, core.Content{Hash: h, Bytes: []byte(bl.content)})
			blocks = append(blocks, map[string]string{"hash": h, "type": bl.typ})
			key.WriteString("|" + bl.content)
		}
		p, _ := json.Marshal(map[string]any{"role": role, "blocks": blocks})
		mh := sha("m-" + key.String())
		contents = append(contents, core.Content{Hash: mh, Bytes: []byte(mh)})
		evs = append(evs, core.StoredEvent{Seq: i + 1, Kind: core.KindMessage, ContentHash: mh, Payload: p})
	}
	if err := e.s.SaveParse(e.t.Context(), id, core.PrincipalLocal, core.ParseStatus(parse), evs, contents); err != nil {
		e.t.Fatal(err)
	}
}

func text(s string) blk { return blk{"text", fmt.Sprintf(`{"type":"text","text":%q}`, s)} }

func sid() string {
	return sessionID(store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: "k"})
}

func TestAPI_TimelineStableIdsAndOrder(t *testing.T) {
	e := newAPIEnv(t)
	e.save("b", "k", "cc", 20) // stored first, starts later
	e.parseMsgs("b", "ok", []any{"user", text("one")}, []any{"assistant", text("two")})
	e.save("a", "k", "cc", 10)
	e.parseMsgs("a", "ok", []any{"user", text("zero")})
	url := "/api/sessions/" + sid() + "/timeline"
	full := e.timeline(url)
	var ids []string
	for _, en := range full.Entries {
		ids = append(ids, en["id"].(string))
	}
	if got := fmt.Sprint(ids); got != "[a:0 a:1 b:0 b:1 b:2]" {
		t.Fatalf("ids = %s", got)
	}
	if full.NextAfter != nil || full.StreamSince != 2 {
		t.Fatalf("next %v since %d", full.NextAfter, full.StreamSince)
	}
	if again := e.get(url).Body.String(); again != e.get(url).Body.String() {
		t.Fatal("two calls differ")
	}
	// Walk in pages of 2: the same ids, no repeat, no skip.
	var walked []string
	p := e.timeline(url + "?limit=2")
	for {
		for _, en := range p.Entries {
			walked = append(walked, en["id"].(string))
		}
		if p.NextAfter == nil {
			break
		}
		p = e.timeline(url + "?limit=2&after=" + *p.NextAfter)
	}
	if fmt.Sprint(walked) != fmt.Sprint(ids) {
		t.Fatalf("walk = %v want %v", walked, ids)
	}
	if full.Entries[0]["kind"] != "model_call" || full.Entries[1]["kind"] != "user_prompt" || full.Entries[4]["kind"] != "assistant_text" {
		t.Fatalf("kinds = %v", full.Entries)
	}
}

func TestAPI_TimelineCutsAt16KiB(t *testing.T) {
	e := newAPIEnv(t)
	e.save("a", "k", "cc", 1)
	e.parseMsgs("a", "ok", []any{"user", text(strings.Repeat("x", 20000))}, []any{"assistant", text("short")})
	p := e.timeline("/api/sessions/" + sid() + "/timeline")
	long, short := p.Entries[1], p.Entries[2]
	if len(long["text"].(string)) != 16*1024 || long["truncated"] != true {
		t.Fatalf("long: len %d truncated %v", len(long["text"].(string)), long["truncated"])
	}
	if short["truncated"] != nil || short["text"] != "short" {
		t.Fatalf("short = %v", short)
	}
}

func TestUI_TimelineMatchesCLIViewer(t *testing.T) {
	e := newAPIEnv(t)
	e.save("a", "k", "cc", 1)
	e.parseMsgs("a", "ok", []any{"user", text("q1")}, []any{"assistant", text("r1"), blk{"reasoning", `{"data":"zzz"}`}})
	e.save("b", "k", "cc", 2)
	e.parseMsgs("b", "ok", []any{"user", text("q1")}, []any{"assistant", text("r1"), blk{"reasoning", `{"data":"zzz"}`}}, []any{"user", text("q2")})
	e.save("c", "k", "cc", 3)
	e.parseMsgs("c", "failed")
	r, err := store.OpenReader(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	cli, err := timeline.Build(r, store.SessionRow{PrincipalID: core.PrincipalLocal, Client: "cc", SessionKey: "k"}, timeline.Options{})
	if err != nil {
		t.Fatal(err)
	}
	api := e.timeline("/api/sessions/" + sid() + "/timeline").Entries
	if len(api) != len(cli) {
		t.Fatalf("api %d entries, cli %d", len(api), len(cli))
	}
	for i, c := range cli {
		a := api[i]
		if a["exchange_id"] != c.RequestID || a["index"] != float64(c.Index) {
			t.Errorf("entry %d: %v vs %+v", i, a, c)
		}
		if c.Text != "" && a["text"] != c.Text {
			t.Errorf("entry %d text %v vs %q", i, a["text"], c.Text)
		}
		if c.Kind == timeline.KindMarker && a["kind"] != "marker" {
			t.Errorf("entry %d kind %v", i, a["kind"])
		}
	}
}

func TestAPI_TimelineFoldedExchangeFlagsMissingUsage(t *testing.T) {
	e := newAPIEnv(t)
	e.save("f", "k", "cc", 1)
	e.parseMsgs("f", "failed")
	p := e.timeline("/api/sessions/" + sid() + "/timeline")
	if len(p.Entries) != 1 || p.Entries[0]["kind"] != "model_call" {
		t.Fatalf("entries = %v", p.Entries)
	}
	u := p.Entries[0]["usage"].(map[string]any)
	if u["has_usage"] != false || u["no_usage"] != "missing" || u["input"] != nil || u["output"] != nil {
		t.Fatalf("usage = %v", u)
	}
}

func TestAPI_TimelineOmitsUnreadableReasoning(t *testing.T) {
	e := newAPIEnv(t)
	e.save("a", "k", "cc", 1)
	e.parseMsgs("a", "ok", []any{"assistant", blk{"reasoning", `{"data":"zzz"}`}})
	e.save("b", "k", "cc", 2)
	e.parseMsgs("b", "ok", []any{"assistant", blk{"reasoning", `{"type":"thinking","thinking":"hmm"}`}, text("hi")})
	p := e.timeline("/api/sessions/" + sid() + "/timeline")
	var thinking []string
	for _, en := range p.Entries {
		if en["kind"] == "thinking" {
			thinking = append(thinking, en["exchange_id"].(string)+"="+en["text"].(string))
		}
		if en["kind"] == "assistant_text" && en["exchange_id"] == "a" && en["text"] != "[reasoning]" {
			t.Errorf("a text = %v", en["text"])
		}
	}
	if fmt.Sprint(thinking) != "[b=hmm]" {
		t.Fatalf("thinking entries = %v", thinking)
	}
}

func TestAPI_ExchangeDetail(t *testing.T) {
	e := newAPIEnv(t)
	ex := &core.Exchange{
		RequestID: "x1", PrincipalID: core.PrincipalLocal, Protocol: "test", Client: "cc",
		Auth: core.AuthAPIKey, Method: "POST", Path: "/v1/x", Status: 200, SessionKey: "k",
		Start: baseTime, End: baseTime.Add(2 * time.Second), HasTTFB: true, TTFB: 500 * time.Millisecond,
		ClientDisconnected: true,
		RequestHeader:      http.Header{"X-A": {"1"}},
		ResponseHeader:     http.Header{"Content-Type": {"application/json"}},
	}
	ex.Response.Truncated = true
	if err := e.s.SaveExchange(t.Context(), ex); err != nil {
		t.Fatal(err)
	}
	e.parse("x1", "m1", `{"input_tokens":10,"output_tokens":5}`)
	rec := e.get("/api/exchanges/x1")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var d map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &d)
	if d["method"] != "POST" || d["status"] != 200.0 || d["model"] != "m1" || d["ttfb_ms"] != 500.0 ||
		d["duration_ms"] != 2000.0 || d["session_id"] != sid() || d["client_disconnected"] != true ||
		d["response_truncated"] != true || d["request_body_stored"] != false {
		t.Fatalf("detail = %v", d)
	}
	if d["request_headers"].(map[string]any)["X-A"] == nil || d["usage"].(map[string]any)["input"] != 10.0 {
		t.Fatalf("headers/usage = %v", d)
	}
}

func (e *apiEnv) saveBody(id string, h http.Header, body []byte, response bool) {
	e.t.Helper()
	ex := &core.Exchange{
		RequestID: id, PrincipalID: core.PrincipalLocal, Protocol: "test", Client: "cc",
		Auth: core.AuthAPIKey, Method: "POST", Path: "/v1/x", Status: 200, Start: baseTime, End: baseTime.Add(time.Second),
	}
	b := core.Body{Chunks: [][]byte{body}, Size: int64(len(body))}
	if response {
		ex.ResponseHeader, ex.Response = h, b
	} else {
		ex.RequestHeader, ex.Request = h, b
	}
	if err := e.s.SaveExchange(e.t.Context(), ex); err != nil {
		e.t.Fatal(err)
	}
}

func TestAPI_BodyTruncatesAt5MiB(t *testing.T) {
	e := newAPIEnv(t)
	big := bytes.Repeat([]byte("a"), 5<<20+10)
	e.saveBody("big", http.Header{"Content-Type": {"text/plain"}}, big, true)
	rec := e.get("/api/exchanges/big/body?part=response")
	if rec.Code != 200 || rec.Body.Len() != 5<<20 || rec.Header().Get("X-Body-Truncated") != "true" ||
		rec.Header().Get("Content-Type") != "text/plain" {
		t.Fatalf("code %d len %d trunc %q ct %q", rec.Code, rec.Body.Len(), rec.Header().Get("X-Body-Truncated"), rec.Header().Get("Content-Type"))
	}
	// Exactly at the cap is whole; a gzip body is returned decompressed.
	e.saveBody("exact", http.Header{"Content-Type": {"text/plain"}}, big[:5<<20], false)
	rec = e.get("/api/exchanges/exact/body?part=request")
	if rec.Body.Len() != 5<<20 || rec.Header().Get("X-Body-Truncated") != "" {
		t.Fatalf("exact: len %d trunc %q", rec.Body.Len(), rec.Header().Get("X-Body-Truncated"))
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(`{"hello":"world"}`))
	_ = zw.Close()
	e.saveBody("gz", http.Header{"Content-Type": {"application/json"}, "Content-Encoding": {"gzip"}}, gz.Bytes(), true)
	rec = e.get("/api/exchanges/gz/body?part=response")
	if rec.Body.String() != `{"hello":"world"}` || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("gzip body = %q", rec.Body)
	}
}

func TestAPI_BodyNotStored404(t *testing.T) {
	e := newAPIEnv(t)
	e.save("n", "", "cc", 1)
	wantEnvelope(t, e.get("/api/exchanges/n/body?part=request"), 404, "body_not_stored")
	wantEnvelope(t, e.get("/api/exchanges/n/body?part=response"), 404, "body_not_stored")
}

func TestAPI_NotFoundAndBadRequestCodes(t *testing.T) {
	e := newAPIEnv(t)
	e.save("a", "k", "cc", 1)
	url := "/api/sessions/" + sid() + "/timeline"
	wantEnvelope(t, e.get("/api/sessions/"+encodeID(t, "local", "cc", "nope")+"/timeline"), 404, "not_found")
	wantEnvelope(t, e.get("/api/sessions/zzz/timeline"), 404, "not_found")
	wantEnvelope(t, e.get("/api/exchanges/missing"), 404, "not_found")
	wantEnvelope(t, e.get("/api/exchanges/missing/body?part=request"), 404, "not_found")
	for _, q := range []string{"?limit=0", "?limit=-1", "?limit=x", "?after=!!", "?since=x", "?since=-1",
		"?after=" + encodeAfter(afterToken{List: listSessions, At: 1})} {
		wantEnvelope(t, e.get(url+q), 400, "bad_request")
	}
	if rec := e.get(url + "?limit=999999"); rec.Code != 200 {
		t.Fatalf("over cap = %d", rec.Code)
	}
	wantEnvelope(t, e.get("/api/exchanges/a/body"), 400, "bad_request")
	wantEnvelope(t, e.get("/api/exchanges/a/body?part=other"), 400, "bad_request")
}
