package timeline_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// fake is an in-memory Source.
type fake struct {
	exchanges []store.ExchangeRow
	events    map[string][]store.EventRow
	contents  map[string][]byte
}

func newFake() *fake {
	return &fake{events: map[string][]store.EventRow{}, contents: map[string][]byte{}}
}

func (f *fake) SessionExchanges(string, string, string) ([]store.ExchangeRow, error) {
	// The real reader orders by start, then request id; the fake returns what it
	// was given, sorted the same way, so ordering tests exercise Build's input.
	return f.exchanges, nil
}

func (f *fake) Events(id string) ([]store.EventRow, error) { return f.events[id], nil }

func (f *fake) Content(hash string) ([]byte, error) {
	b, ok := f.contents[hash]
	if !ok {
		return nil, fmt.Errorf("content %s: %w", hash, store.ErrNotFound)
	}
	return b, nil
}

func hashOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func (f *fake) put(data string) string {
	h := hashOf(data)
	f.contents[h] = []byte(data)
	return h
}

// exchange adds an exchange; the events follow with add*.
func (f *fake) exchange(id string, start int64, parse string) *store.ExchangeRow {
	f.exchanges = append(f.exchanges, store.ExchangeRow{RequestID: id, StartedAt: start,
		Method: "POST", Path: "/v1/x", Status: 200, Parse: parse})
	return &f.exchanges[len(f.exchanges)-1]
}

func (f *fake) add(id string, e store.EventRow) {
	e.RequestID = id
	e.Seq = len(f.events[id])
	if e.Payload == nil {
		e.Payload = json.RawMessage(`{}`)
	}
	f.events[id] = append(f.events[id], e)
}

// msg adds a text message; its hash is the hash of role and text, so equal
// messages in different exchanges have equal hashes.
func (f *fake) msg(id, role, text string) {
	block := f.put(fmt.Sprintf(`{"type":"text","text":%q}`, text))
	payload, _ := json.Marshal(map[string]any{"role": role,
		"blocks": []map[string]string{{"hash": block, "type": "text"}}})
	f.add(id, store.EventRow{Kind: "message", ContentHash: hashOf(role + "|" + text), Payload: payload})
}

func (f *fake) toolCall(id, callID, name, input string) {
	payload, _ := json.Marshal(map[string]any{"name": name, "id": callID})
	f.add(id, store.EventRow{Kind: "tool_call", ToolCallID: callID, ContentHash: f.put(input), Payload: payload})
}

func (f *fake) toolResult(id, callID, result string, isErr bool) {
	payload, _ := json.Marshal(map[string]any{"is_error": isErr})
	f.add(id, store.EventRow{Kind: "tool_result", ToolCallID: callID, ContentHash: f.put(result), Payload: payload})
}

// session adds a thread of messages, "role:text" strings, as one exchange.
func (f *fake) thread(id string, start int64, msgs ...string) {
	f.exchange(id, start, "ok")
	for _, m := range msgs {
		role, text := m[:1], m[2:]
		if role == "u" {
			role = "user"
		} else {
			role = "assistant"
		}
		f.msg(id, role, text)
	}
}

func build(t *testing.T, f *fake, opts timeline.Options) []timeline.Entry {
	t.Helper()
	out, err := timeline.Build(f, store.SessionRow{PrincipalID: "p", Client: "c", SessionKey: "k"}, opts)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return out
}

// texts lists the message texts of the entries of one exchange, with "!" for a marker.
func texts(entries []timeline.Entry, id string) []string {
	var out []string
	for _, e := range entries {
		if e.RequestID != id {
			continue
		}
		switch e.Kind {
		case timeline.KindMessage:
			out = append(out, e.Text)
		case timeline.KindMarker:
			out = append(out, "!")
		}
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
