package ui

import (
	"errors"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/effects"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

const (
	defaultEffectsLimit = 500
	maxEffectsLimit     = 5000
)

// effectJSON is one effect as the web UI reads it. Facts are computed here; the browser
// only places and displays them.
type effectJSON struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	At         string `json:"at"`
	Path       string `json:"path,omitempty"`
	Change     string `json:"change,omitempty"`
	Size       *int64 `json:"size,omitempty"`
	HasCopy    bool   `json:"has_copy"`
	HasDiff    bool   `json:"has_diff"`
	Detail     string `json:"detail,omitempty"`
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port,omitempty"`
	Via        string `json:"via,omitempty"`
	BytesUp    *int64 `json:"bytes_up,omitempty"`
	BytesDown  *int64 `json:"bytes_down,omitempty"`
	DurationMS *int64 `json:"duration_ms,omitempty"`
	Outcome    string `json:"outcome,omitempty"`
}

// effectVersionJSON is a session's effect version.
type effectVersionJSON struct {
	Count int   `json:"count"`
	MaxID int64 `json:"max_id"`
}

// effectCountsJSON is a session's effect counts on the sessions list.
type effectCountsJSON struct {
	Changed int `json:"changed"`
	Read    int `json:"read"`
	Sites   int `json:"sites"`
}

type effectsPage struct {
	Effects []effectJSON       `json:"effects"`
	Version *effectVersionJSON `json:"version,omitempty"`
}

func mountEffects(api chi.Router, d Deps) {
	api.Get("/sessions/{id}/effects", d.listEffects)
	api.Get("/effects/unassigned", d.listUnassigned)
	api.Get("/effects/{eid}/content", d.effectContent)
	api.Get("/effects/{eid}/diff", d.effectDiff)
}

// hasContent reports whether an effect's content can be served: a file change's copy,
// or a read whose hook payload is stored and whose client can read it.
func (d Deps) hasContent(e store.EffectRow) bool {
	switch e.Kind {
	case string(core.EffectFileChange):
		return e.AfterHash != ""
	case string(core.EffectFileRead):
		_, ok := d.Rules[e.Client].(core.EffectRules)
		return ok && e.HookPayload != ""
	}
	return false
}

// hasDiff reports whether a file change has a before version to diff against.
func hasDiff(e store.EffectRow) bool {
	return e.Kind == string(core.EffectFileChange) && e.BeforeHash != "" &&
		(e.AfterHash != "" || e.Change == string(core.FileDeleted))
}

func (d Deps) effectToJSON(e store.EffectRow) effectJSON {
	out := effectJSON{
		ID: e.ID, Kind: e.Kind, At: nsTime(e.At), Path: e.Path, Change: e.Change,
		HasCopy: d.hasContent(e), HasDiff: hasDiff(e), Detail: e.Detail,
		Host: e.Host, Port: e.Port, Via: e.Via, Outcome: e.Outcome,
	}
	switch e.Kind {
	case string(core.EffectFileChange):
		size := e.Size
		out.Size = &size
	case string(core.EffectNetConn):
		up, down := e.BytesUp, e.BytesDown
		out.BytesUp, out.BytesDown = &up, &down
		if e.EndedAt > 0 {
			ms := (e.EndedAt - e.At) / 1e6
			out.DurationMS = &ms
		}
	}
	return out
}

// parseAfterID reads ?after= as an effect id; absent is 0.
func parseAfterID(r *http.Request) (int64, bool) {
	if !r.URL.Query().Has("after") {
		return 0, true
	}
	n, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	return n, err == nil && n >= 0
}

func (d Deps) listEffects(w http.ResponseWriter, r *http.Request) {
	principal, client, key, ok := parseSessionID(chi.URLParam(r, "id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "no such session")
		return
	}
	after, ok := parseAfterID(r)
	limit, okLimit := parseLimit(r, defaultEffectsLimit, maxEffectsLimit)
	if !ok || !okLimit {
		writeErr(w, http.StatusBadRequest, "bad_request", "after and limit must be integers")
		return
	}
	ctx, cancel := storeCtx(r)
	defer cancel()
	c := d.Reader.WithContext(ctx)
	ident := store.SessionIdent{Principal: principal, Client: client, Key: key}
	vers, err := c.EffectVersions([]store.SessionIdent{ident})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	rows, err := c.ListEffects(ident, after, limit)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	v := vers[ident]
	page := effectsPage{Effects: make([]effectJSON, 0, len(rows)), Version: &effectVersionJSON{Count: v.Count, MaxID: v.MaxID}}
	for _, e := range rows {
		page.Effects = append(page.Effects, d.effectToJSON(e))
	}
	writeJSON(w, page)
}

func (d Deps) listUnassigned(w http.ResponseWriter, r *http.Request) {
	after, ok := parseAfterID(r)
	limit, okLimit := parseLimit(r, defaultEffectsLimit, maxEffectsLimit)
	if !ok || !okLimit {
		writeErr(w, http.StatusBadRequest, "bad_request", "after and limit must be integers")
		return
	}
	ctx, cancel := storeCtx(r)
	defer cancel()
	rows, err := d.Reader.WithContext(ctx).ListUnassigned(after, limit)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	page := effectsPage{Effects: make([]effectJSON, 0, len(rows))}
	for _, e := range rows {
		page.Effects = append(page.Effects, d.effectToJSON(e))
	}
	writeJSON(w, page)
}

// effectRow reads the effect named in the route, answering 404 itself when there is none.
func (d Deps) effectRow(w http.ResponseWriter, r *http.Request, c *store.CtxReader) (store.EffectRow, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "eid"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusNotFound, "not_found", "no such effect")
		return store.EffectRow{}, false
	}
	e, err := c.Effect(id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "no such effect")
		return store.EffectRow{}, false
	}
	if err != nil {
		writeStoreErr(w, err)
		return store.EffectRow{}, false
	}
	return e, true
}

// contentJSON is a file's stored content or a read's returned content.
type contentJSON struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Valid   bool   `json:"utf8"`
}

func (d Deps) effectContent(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := storeCtx(r)
	defer cancel()
	c := d.Reader.WithContext(ctx)
	e, ok := d.effectRow(w, r, c)
	if !ok {
		return
	}
	if !d.hasContent(e) {
		writeErr(w, http.StatusNotFound, "no_content", noContentReason(e))
		return
	}
	var body []byte
	var err error
	switch e.Kind {
	case string(core.EffectFileChange):
		body, err = c.Content(e.AfterHash)
	default:
		var payload []byte
		if payload, err = c.Content(e.HookPayload); err == nil {
			var found bool
			if body, found = d.Rules[e.Client].(core.EffectRules).ReadContent(payload); !found {
				writeErr(w, http.StatusNotFound, "no_content", "the stored hook event holds no content for this read")
				return
			}
		}
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, contentJSON{Path: e.Path, Content: string(body), Valid: utf8.Valid(body)})
}

// noContentReason says why an effect has no content to show.
func noContentReason(e store.EffectRow) string {
	switch e.Kind {
	case string(core.EffectFileChange):
		if e.Change == string(core.FileDeleted) {
			return "the file was deleted"
		}
		return "no copy is stored: the file is binary or over max_copy_bytes"
	case string(core.EffectFileRead):
		return "the hook event behind this read is not stored"
	}
	return "network effects have no content"
}

// diffJSON is a server-computed unified line diff.
type diffJSON struct {
	Path      string `json:"path"`
	Diff      string `json:"diff"`
	Truncated bool   `json:"truncated"`
}

func (d Deps) effectDiff(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := storeCtx(r)
	defer cancel()
	c := d.Reader.WithContext(ctx)
	e, ok := d.effectRow(w, r, c)
	if !ok {
		return
	}
	if !hasDiff(e) {
		reason := "no earlier version is stored; open the content instead"
		if e.Kind == string(core.EffectFileChange) && e.AfterHash == "" && e.Change != string(core.FileDeleted) {
			reason = noContentReason(e)
		} else if e.Kind != string(core.EffectFileChange) {
			reason = "only file changes have a diff"
		}
		writeErr(w, http.StatusNotFound, "no_diff", reason)
		return
	}
	before, err := c.Content(e.BeforeHash)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	var after []byte
	if e.AfterHash != "" {
		if after, err = c.Content(e.AfterHash); err != nil {
			writeStoreErr(w, err)
			return
		}
	}
	text, truncated := effects.Diff(string(before), string(after), effects.DiffMaxLines)
	writeJSON(w, diffJSON{Path: e.Path, Diff: text, Truncated: truncated})
}
