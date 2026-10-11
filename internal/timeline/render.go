package timeline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

func stamp(ns int64, loc *time.Location) string {
	return time.Unix(0, ns).In(loc).Format(time.RFC3339)
}

// escape makes s safe to print on a terminal: every control character, C0, DEL and
// C1, and every invalid byte, becomes a visible escape. keepLayout leaves \n and \t.
func escape(s string, keepLayout bool) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case keepLayout && (r == '\n' || r == '\t'):
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r >= 0x80 && r <= 0x9f:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

// RenderSessionsText prints one row per session with times in loc as RFC 3339.
func RenderSessionsText(w io.Writer, rows []SessionListing, loc *time.Location) error {
	var buf bytes.Buffer
	for _, r := range rows {
		fmt.Fprintf(&buf, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			escape(r.SessionKey, false), escape(r.Client, false), escape(r.PrincipalID, false),
			stamp(r.First, loc), stamp(r.Last, loc), r.Count, escape(r.Preview, false),
			groupText(r.Usage.Total, r.Usage.Calls))
		if len(r.Usage.Models) > 1 {
			for _, m := range r.Usage.Models {
				fmt.Fprintf(&buf, "   %s: %s\n", escape(m.Model, false), groupText(m.Group, m.Calls))
			}
		}
	}
	_, err := w.Write(buf.Bytes())
	return err
}

type sessionJSON struct {
	Type      string `json:"type"`
	Key       string `json:"session_key"`
	Client    string `json:"client"`
	Principal string `json:"principal_id"`
	First     string `json:"first"`
	Last      string `json:"last"`
	Count     int    `json:"count"`
	Preview   string `json:"preview"`
	summaryJSON
}

// RenderSessionsJSON prints one JSON object per session, times in loc (UTC for the
// viewer).
func RenderSessionsJSON(w io.Writer, rows []SessionListing, loc *time.Location) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range rows {
		if err := enc.Encode(sessionJSON{Type: "session", Key: r.SessionKey, Client: r.Client,
			Principal: r.PrincipalID, First: stamp(r.First, loc), Last: stamp(r.Last, loc),
			Count: r.Count, Preview: r.Preview, summaryJSON: summaryObject("", r.Usage)}); err != nil {
			return err
		}
	}
	_, err := w.Write(buf.Bytes())
	return err
}

func exchangeLine(e Entry, loc *time.Location) string {
	s := fmt.Sprintf("%s %s %s %d", stamp(e.Start, loc), escape(e.Method, false), escape(e.Path, false), e.Status)
	if e.Parse != "" && e.Parse != "ok" {
		s += " parse=" + escape(e.Parse, false)
	}
	return s + " " + escape(e.RequestID, false)
}

func counterText(v *int64) string {
	if v == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d", *v)
}

// callSuffix is the text appended to a model call's line; empty for a non-call.
func callSuffix(c *CallUsage) string {
	switch {
	case c == nil:
		return ""
	case !c.HasUsage:
		return " no usage (" + c.NoUsage + ")"
	}
	s := fmt.Sprintf(" tokens in=%s out=%s cache_write=%s cache_read=%s",
		counterText(c.Input), counterText(c.Output), counterText(c.CacheWrite), counterText(c.CacheRead))
	if c.Partial {
		s += " [usage partial]"
	}
	return s
}

// RenderText prints entries as text with times in loc as RFC 3339. Message and tool
// bodies keep newlines and tabs; every other control character is escaped.
func RenderText(w io.Writer, entries []Entry, loc *time.Location) error {
	var b bytes.Buffer
	for _, e := range entries {
		mark := ""
		if e.Partial {
			mark = " (partial)"
		}
		switch e.Kind {
		case KindExchange:
			fmt.Fprintf(&b, "== %s%s%s\n", exchangeLine(e, loc), mark, callSuffix(e.Call))
		case KindFolded:
			fmt.Fprintf(&b, "-- %s%s\n", exchangeLine(e, loc), callSuffix(e.Call))
		case KindMarker:
			fmt.Fprintf(&b, "   ~ %s\n", escape(e.Text, false))
		case KindMessage:
			fmt.Fprintf(&b, "  %s%s: %s\n", escape(e.Role, false), mark, escape(e.Text, true))
		case KindToolCall:
			fmt.Fprintf(&b, "  tool_call %s (%s)%s: %s\n", escape(e.Name, false), escape(e.ToolCallID, false), mark, escape(e.Text, true))
		case KindToolResult:
			errMark := ""
			if e.IsError {
				errMark = " error"
			}
			fmt.Fprintf(&b, "  tool_result (%s)%s%s: %s\n", escape(e.ToolCallID, false), errMark, mark, escape(e.Text, true))
		case KindError:
			fmt.Fprintf(&b, "  error %d %s%s: %s\n", e.Status, escape(e.Name, false), mark, escape(e.Text, true))
		}
		if e.Omitted > 0 {
			fmt.Fprintf(&b, "  [%d bytes omitted]\n", e.Omitted)
		}
	}
	_, err := w.Write(b.Bytes())
	return err
}

type entryJSON struct {
	Type       string `json:"type"`
	RequestID  string `json:"request_id"`
	Time       string `json:"time"`
	Method     string `json:"method,omitempty"`
	Path       string `json:"path,omitempty"`
	Status     int    `json:"status,omitempty"`
	Parse      string `json:"parse,omitempty"`
	Role       string `json:"role,omitempty"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`
	Text       string `json:"text,omitempty"`
	Omitted    int    `json:"omitted_bytes,omitempty"`
	Partial    bool   `json:"partial,omitempty"`
	// Usage is the counters object, or null with NoUsage; omitted for a non-call.
	Usage   json.RawMessage `json:"usage,omitempty"`
	NoUsage string          `json:"no_usage,omitempty"`
}

type callUsageJSON struct {
	Input      *int64 `json:"input_tokens"`
	Output     *int64 `json:"output_tokens"`
	CacheWrite *int64 `json:"cache_write_tokens"`
	CacheRead  *int64 `json:"cache_read_tokens"`
	Partial    bool   `json:"partial"`
}

func usageFields(c *CallUsage) (json.RawMessage, string, error) {
	switch {
	case c == nil:
		return nil, "", nil
	case !c.HasUsage:
		return json.RawMessage("null"), c.NoUsage, nil
	}
	raw, err := json.Marshal(callUsageJSON{c.Input, c.Output, c.CacheWrite, c.CacheRead, c.Partial})
	return raw, "", err
}

// RenderJSON prints one JSON object per entry with a type field and the time in loc
// (UTC for the viewer).
func RenderJSON(w io.Writer, entries []Entry, loc *time.Location) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, e := range entries {
		usage, noUsage, err := usageFields(e.Call)
		if err != nil {
			return err
		}
		if err := enc.Encode(entryJSON{Usage: usage, NoUsage: noUsage, Type: string(e.Kind), RequestID: e.RequestID, Time: stamp(e.Start, loc),
			Method: e.Method, Path: e.Path, Status: e.Status, Parse: e.Parse, Role: e.Role, Name: e.Name,
			ToolCallID: e.ToolCallID, IsError: e.IsError, Text: e.Text, Omitted: e.Omitted, Partial: e.Partial}); err != nil {
			return err
		}
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// UngroupedSummary is the row that ends the sessions list: the exchanges that carry
// no session key, with the usage of their model calls.
type UngroupedSummary struct {
	Count       int
	First, Last int64
	Usage       Summary
}

// RenderUngroupedRowText prints the ungrouped row in the sessions columns.
func RenderUngroupedRowText(w io.Writer, u UngroupedSummary, loc *time.Location) error {
	_, err := fmt.Fprintf(w, "ungrouped\t\t\t%s\t%s\t%d\t\t%s\n", stamp(u.First, loc), stamp(u.Last, loc), u.Count,
		groupText(u.Usage.Total, u.Usage.Calls))
	return err
}

// RenderUngroupedRowJSON prints the ungrouped row as one JSON object.
func RenderUngroupedRowJSON(w io.Writer, u UngroupedSummary, loc *time.Location) error {
	return json.NewEncoder(w).Encode(struct {
		Type  string `json:"type"`
		First string `json:"first"`
		Last  string `json:"last"`
		Count int    `json:"count"`
		summaryJSON
	}{"ungrouped", stamp(u.First, loc), stamp(u.Last, loc), u.Count, summaryObject("", u.Usage)})
}

// UngroupedExchange is one exchange with no session key; Call is set when it is a
// model call.
type UngroupedExchange struct {
	RequestID, Client, Method, Path string
	Start                           int64
	Status                          int
	Call                            *CallUsage
}

// UngroupedList is what `sessions -ungrouped` prints: the listed rows, and the usage
// of all Exchanges ungrouped exchanges, whatever the row limit.
type UngroupedList struct {
	Rows      []UngroupedExchange
	Exchanges int
	Usage     Summary
}

// RenderUngroupedText prints one exchange per line: request id, time, client,
// method, path, status and the call's tokens, then the usage total.
func RenderUngroupedText(w io.Writer, l UngroupedList, loc *time.Location) error {
	var buf bytes.Buffer
	for _, r := range l.Rows {
		fmt.Fprintf(&buf, "%s\t%s\t%s\t%s\t%s\t%d%s\n", escape(r.RequestID, false), stamp(r.Start, loc),
			escape(r.Client, false), escape(r.Method, false), escape(r.Path, false), r.Status, callSuffix(r.Call))
	}
	fmt.Fprintf(&buf, "total over all %d ungrouped exchanges\n", l.Exchanges)
	if err := RenderUsageText(&buf, l.Usage); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// RenderUngroupedJSON prints one JSON object per exchange, then an ungrouped_usage
// object.
func RenderUngroupedJSON(w io.Writer, l UngroupedList, loc *time.Location) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range l.Rows {
		usage, noUsage, err := usageFields(r.Call)
		if err != nil {
			return err
		}
		if err := enc.Encode(struct {
			Type      string          `json:"type"`
			RequestID string          `json:"request_id"`
			Time      string          `json:"time"`
			Client    string          `json:"client"`
			Method    string          `json:"method"`
			Path      string          `json:"path"`
			Status    int             `json:"status"`
			Usage     json.RawMessage `json:"usage,omitempty"`
			NoUsage   string          `json:"no_usage,omitempty"`
		}{"exchange", r.RequestID, stamp(r.Start, loc), r.Client, r.Method, r.Path, r.Status, usage, noUsage}); err != nil {
			return err
		}
	}
	if err := enc.Encode(struct {
		summaryJSON
		Exchanges int `json:"exchanges"`
	}{summaryObject("ungrouped_usage", l.Usage), l.Exchanges}); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// RenderSessionHeaderText prints the line that heads one session's timeline when
// several sessions share a key.
func RenderSessionHeaderText(w io.Writer, client, principal, key string) error {
	_, err := fmt.Fprintf(w, "### session %s client=%s principal=%s\n", escape(key, false), escape(client, false), escape(principal, false))
	return err
}

// groupText is the one text form of a usage group, used by every view.
func groupText(g Group, calls int) string {
	switch {
	case calls == 0:
		return "no model calls"
	case g.AllErrored():
		return fmt.Sprintf("no usage (%d errored)", g.Errored)
	}
	s := fmt.Sprintf("input %s, output %s, cache-write %s, cache-read %s",
		counterText(g.Input.Value), counterText(g.Output.Value),
		counterText(g.CacheWrite.Value), counterText(g.CacheRead.Value))
	if g.PartialCalls > 0 {
		s += fmt.Sprintf("; partial (%d calls)", g.PartialCalls)
	}
	if g.Missing > 0 {
		s += fmt.Sprintf("; incomplete: %d of %d model calls missing usage", g.Missing, g.Reporting+g.Missing)
	}
	for _, c := range []struct {
		name string
		sum  Sum
	}{{"input", g.Input}, {"output", g.Output}, {"cache-write", g.CacheWrite}, {"cache-read", g.CacheRead}} {
		if c.sum.Unreported > 0 {
			s += fmt.Sprintf("; %s incomplete (%d calls did not report)", c.name, c.sum.Unreported)
		}
	}
	if g.Errored > 0 {
		s += fmt.Sprintf("; %d errored", g.Errored)
	}
	return s
}

// RenderUsageText prints a session's usage total and one indented line per model.
func RenderUsageText(w io.Writer, s Summary) error {
	var b bytes.Buffer
	fmt.Fprintf(&b, "== usage total: %s\n", groupText(s.Total, s.Calls))
	for _, m := range s.Models {
		fmt.Fprintf(&b, "   %s: %s\n", escape(m.Model, false), groupText(m.Group, m.Calls))
	}
	_, err := w.Write(b.Bytes())
	return err
}

type unreportedJSON struct {
	Input      int `json:"input_tokens"`
	Output     int `json:"output_tokens"`
	CacheWrite int `json:"cache_write_tokens"`
	CacheRead  int `json:"cache_read_tokens"`
}

type groupJSON struct {
	Input        *int64         `json:"input_tokens"`
	Output       *int64         `json:"output_tokens"`
	CacheWrite   *int64         `json:"cache_write_tokens"`
	CacheRead    *int64         `json:"cache_read_tokens"`
	Calls        int            `json:"calls"`
	Missing      int            `json:"missing"`
	Errored      int            `json:"errored"`
	PartialCalls int            `json:"partial_calls"`
	Unreported   unreportedJSON `json:"unreported"`
	Complete     bool           `json:"complete"`
}

// usageGroup is the group object, or null (with sibling errored and calls) when the
// group has no model calls or every call errored.
func usageGroup(g Group, calls int) *groupJSON {
	if calls == 0 || g.AllErrored() {
		return nil
	}
	return &groupJSON{g.Input.Value, g.Output.Value, g.CacheWrite.Value, g.CacheRead.Value,
		calls, g.Missing, g.Errored, g.PartialCalls,
		unreportedJSON{g.Input.Unreported, g.Output.Unreported, g.CacheWrite.Unreported, g.CacheRead.Unreported},
		!g.Incomplete()}
}

type modelUsageJSON struct {
	Model   string     `json:"model"`
	Usage   *groupJSON `json:"usage"`
	Errored int        `json:"errored"`
	Calls   int        `json:"calls"`
}

type summaryJSON struct {
	Type    string           `json:"type,omitempty"`
	Usage   *groupJSON       `json:"usage"`
	Errored int              `json:"errored"`
	Calls   int              `json:"calls"`
	Models  []modelUsageJSON `json:"models"`
}

func summaryObject(typ string, s Summary) summaryJSON {
	o := summaryJSON{Type: typ, Usage: usageGroup(s.Total, s.Calls), Errored: s.Total.Errored,
		Calls: s.Calls, Models: []modelUsageJSON{}}
	for _, m := range s.Models {
		o.Models = append(o.Models, modelUsageJSON{m.Model, usageGroup(m.Group, m.Calls), m.Errored, m.Calls})
	}
	return o
}

// RenderUsageJSON prints a session's usage total as one usage_total object.
func RenderUsageJSON(w io.Writer, s Summary) error {
	return json.NewEncoder(w).Encode(summaryObject("usage_total", s))
}
