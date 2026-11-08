package timeline

import (
	"encoding/json"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// sourceResponse marks the stored events parsed from the provider's reply.
const sourceResponse = "response"

// RequestMessage is one message of a request's history, its text cut to the limit.
type RequestMessage struct {
	Role      string
	Text      string
	Truncated bool
}

// Detail is what one exchange's stored events say about its request and reply.
type Detail struct {
	// Messages are the request's own messages, in order, without the reply.
	Messages []RequestMessage
	// HasResponse is true when a reply message or tool call was parsed.
	HasResponse bool
	// ResponseText joins the reply's message texts; StopReason is the reply's, "" when
	// the provider gave none.
	ResponseText string
	StopReason   string
	// ResponseToolCallIDs are the ids of the tool calls the reply made.
	ResponseToolCallIDs []string
}

// ExchangeDetail reads one exchange's events. A folded exchange (parse failed or
// skipped) has none, so every field is empty. Text is cut to limit bytes.
func ExchangeDetail(src Source, ex store.ExchangeRow, limit int) (Detail, error) {
	evs, err := loadEvents(src, ex)
	if err != nil {
		return Detail{}, err
	}
	var d Detail
	var reply []string
	for _, e := range evs {
		switch e.Kind {
		case "message":
			var en Entry
			if err := fillMessage(src, e, &en); err != nil {
				return Detail{}, err
			}
			text, omitted := cut(en.Text, limit)
			if e.Source == sourceResponse {
				d.HasResponse = true
				reply = append(reply, en.Text)
				d.StopReason = stopReasonOf(e.Payload, d.StopReason)
				continue
			}
			d.Messages = append(d.Messages, RequestMessage{Role: en.Role, Text: text, Truncated: omitted > 0})
		case "tool_call":
			if e.Source == sourceResponse {
				d.HasResponse = true
				d.ResponseToolCallIDs = append(d.ResponseToolCallIDs, e.ToolCallID)
			}
		}
	}
	d.ResponseText, _ = cut(strings.Join(reply, "\n"), limit)
	return d, nil
}

func stopReasonOf(payload []byte, prev string) string {
	var p struct {
		StopReason string `json:"stop_reason"`
	}
	if json.Unmarshal(payload, &p) == nil && p.StopReason != "" {
		return p.StopReason
	}
	return prev
}
