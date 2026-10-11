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
		fmt.Fprintf(&buf, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n",
			escape(r.SessionKey, false), escape(r.Client, false), escape(r.PrincipalID, false),
			stamp(r.First, loc), stamp(r.Last, loc), r.Count, escape(r.Preview, false))
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
}

// RenderSessionsJSON prints one JSON object per session, times in loc (UTC for the
// viewer).
func RenderSessionsJSON(w io.Writer, rows []SessionListing, loc *time.Location) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, r := range rows {
		if err := enc.Encode(sessionJSON{Type: "session", Key: r.SessionKey, Client: r.Client,
			Principal: r.PrincipalID, First: stamp(r.First, loc), Last: stamp(r.Last, loc),
			Count: r.Count, Preview: r.Preview}); err != nil {
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
			fmt.Fprintf(&b, "== %s%s\n", exchangeLine(e, loc), mark)
		case KindFolded:
			fmt.Fprintf(&b, "-- %s\n", exchangeLine(e, loc))
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
}

// RenderJSON prints one JSON object per entry with a type field and the time in loc
// (UTC for the viewer).
func RenderJSON(w io.Writer, entries []Entry, loc *time.Location) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, e := range entries {
		if err := enc.Encode(entryJSON{Type: string(e.Kind), RequestID: e.RequestID, Time: stamp(e.Start, loc),
			Method: e.Method, Path: e.Path, Status: e.Status, Parse: e.Parse, Role: e.Role, Name: e.Name,
			ToolCallID: e.ToolCallID, IsError: e.IsError, Text: e.Text, Omitted: e.Omitted, Partial: e.Partial}); err != nil {
			return err
		}
	}
	_, err := w.Write(buf.Bytes())
	return err
}
