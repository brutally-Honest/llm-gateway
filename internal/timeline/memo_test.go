package timeline_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// memoSource is a fake that counts Content reads and may implement TextMemo.
type memoSource struct {
	*fake
	reads   int
	failing bool
	memo    map[[2]string][2]any
}

func (m *memoSource) Content(h string) ([]byte, error) {
	m.reads++
	if m.failing {
		return nil, errors.New("disk")
	}
	return m.fake.Content(h)
}

func (m *memoSource) MemoText(hash, role string) (string, bool, bool) {
	v, found := m.memo[[2]string{hash, role}]
	if !found {
		return "", false, false
	}
	return v[0].(string), v[1].(bool), true
}

func (m *memoSource) PutText(hash, role, text string, ok bool) {
	m.memo[[2]string{hash, role}] = [2]any{text, ok}
}

// plainSource hides the memo methods.
type plainSource struct{ timeline.Source }

func memoEvent(f *fake, role, text string) store.EventRow {
	f.exchange("r", 1, "ok")
	f.msg("r", role, text)
	return f.events["r"][0]
}

func newMemoSource() *memoSource {
	return &memoSource{fake: newFake(), memo: map[[2]string][2]any{}}
}

func TestTextOf_MemoHitSkipsContent(t *testing.T) {
	m := newMemoSource()
	e := memoEvent(m.fake, "user", "hello")
	for i := range 3 {
		text, ok, err := timeline.TextOf(m, e, "user")
		if err != nil || !ok || text != "hello" {
			t.Fatalf("call %d = %q %v %v", i, text, ok, err)
		}
	}
	if m.reads != 1 {
		t.Fatalf("content reads = %d, want 1", m.reads)
	}
	// An event with a memoised hash is answered without decoding its payload.
	e.Payload = json.RawMessage(`not json`)
	if text, ok, _ := timeline.TextOf(m, e, "user"); !ok || text != "hello" {
		t.Fatalf("hit = %q %v", text, ok)
	}
}

func TestTextOf_DecodeFailureNotMemoised(t *testing.T) {
	m := newMemoSource()
	e := memoEvent(m.fake, "user", "hello")
	good := e.Payload
	e.Payload = json.RawMessage(`not json`)
	if text, ok, err := timeline.TextOf(m, e, "user"); text != "" || ok || err != nil {
		t.Fatalf("bad payload = %q %v %v", text, ok, err)
	}
	if len(m.memo) != 0 {
		t.Fatalf("memo = %v", m.memo)
	}
	e.Payload = good
	if text, ok, _ := timeline.TextOf(m, e, "user"); !ok || text != "hello" {
		t.Fatalf("after = %q %v", text, ok)
	}
}

func TestTextOf_ContentErrorNotMemoised(t *testing.T) {
	m := newMemoSource()
	e := memoEvent(m.fake, "user", "hello")
	m.failing = true
	if _, _, err := timeline.TextOf(m, e, "user"); err == nil {
		t.Fatal("want the content error")
	}
	if len(m.memo) != 0 {
		t.Fatalf("memo = %v", m.memo)
	}
	m.failing = false
	if text, ok, err := timeline.TextOf(m, e, "user"); err != nil || !ok || text != "hello" {
		t.Fatalf("after = %q %v %v", text, ok, err)
	}
}

func TestTextOf_RoleMismatchMemoised(t *testing.T) {
	m := newMemoSource()
	e := memoEvent(m.fake, "assistant", "hello")
	if text, ok, err := timeline.TextOf(m, e, "user"); text != "" || ok || err != nil {
		t.Fatalf("mismatch = %q %v %v", text, ok, err)
	}
	if v, found := m.memo[[2]string{e.ContentHash, "user"}]; !found || v[0] != "" || v[1] != false {
		t.Fatalf("memo = %v", m.memo)
	}
	if _, found := m.memo[[2]string{e.ContentHash, "assistant"}]; found {
		t.Fatal("other role memoised")
	}
	if text, ok, _ := timeline.TextOf(m, e, "assistant"); !ok || text != "hello" {
		t.Fatalf("assistant = %q %v", text, ok)
	}
}

func TestTextOf_NoMemoUnchanged(t *testing.T) {
	m := newMemoSource()
	e := memoEvent(m.fake, "user", "hello")
	p := plainSource{m}
	for range 2 {
		if text, ok, err := timeline.TextOf(p, e, "user"); err != nil || !ok || text != "hello" {
			t.Fatalf("= %q %v %v", text, ok, err)
		}
	}
	if m.reads != 2 || len(m.memo) != 0 {
		t.Fatalf("reads = %d memo = %v", m.reads, m.memo)
	}
	// A memo is not used for an event without a content hash or a non-message.
	e.ContentHash = ""
	_, _, _ = timeline.TextOf(m, e, "user")
	_, _, _ = timeline.TextOf(m, e, "user")
	e.ContentHash, e.Kind = "x", "tool_call"
	_, _, _ = timeline.TextOf(m, e, "user")
	if len(m.memo) != 0 {
		t.Fatalf("memo = %v", m.memo)
	}
}
