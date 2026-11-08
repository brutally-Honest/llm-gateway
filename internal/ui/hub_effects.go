package ui

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// effectsEventJSON is the live effects event: a session's effect version and counts, or,
// with no session, the count of unassigned connections. The browser then fetches the
// effects after the last id it holds; the timeline is never rebuilt for it.
type effectsEventJSON struct {
	Session    string             `json:"session"`
	Version    *effectVersionJSON `json:"version,omitempty"`
	Counts     *effectCountsJSON  `json:"counts,omitempty"`
	Unassigned *int               `json:"unassigned_connections,omitempty"`
}

// effReadFn reads what an effects event carries. Tests replace it.
type effReadFn func(ctx context.Context, who sessIdent) (effectsEventJSON, error)

// PublishEffects notes that effects of this session were stored; an empty sessionKey is
// the unassigned effects. Effects of one session inside effectsEvery share one event,
// read when the window ends, so the last effect always lands. It never waits, and does
// nothing when nobody watches the session. The timeline cache is never touched.
func (h *Hub) PublishEffects(principal, client, sessionKey string) {
	who := sessIdent{principal, client, sessionKey}
	if sessionKey == "" {
		who = sessIdent{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-h.done:
		return
	default:
	}
	if _, ok := h.effPending[who]; ok || !h.effWatchedLocked(who) {
		return
	}
	h.effPending[who] = time.AfterFunc(h.effEvery, func() {
		logging.Go(h.log, "ui_hub_effects", func() error { h.sendEffects(who); return nil })
	})
}

// effWatchedLocked reports whether any stream wants who's effects events: the global
// streams always, a session stream for its own session. h.mu is held.
func (h *Hub) effWatchedLocked(who sessIdent) bool {
	if who.name == "" {
		for s := range h.subs {
			if s.session == "" {
				return true
			}
		}
		return false
	}
	return h.watchedLocked(who.id())
}

// sendEffects reads and sends one session's effects event.
func (h *Hub) sendEffects(who sessIdent) {
	h.mu.Lock()
	delete(h.effPending, who)
	h.mu.Unlock()
	select {
	case <-h.done:
		return
	default:
	}
	ctx, cancel := context.WithTimeout(h.ctx, storeTimeout)
	ev, err := h.effRead(ctx, who)
	cancel()
	if err != nil {
		if h.ctx.Err() == nil {
			h.log.Warn("ui hub effects read failed", zap.Error(err))
		}
		return
	}
	targets := h.targets("", true)
	if who.name != "" {
		targets = append(targets, h.targets(who.id(), false)...)
	}
	m := liveMsg{Effects: &ev}
	for _, s := range targets {
		h.send(s, m)
	}
}

// readEffectsEvent is the production effReadFn.
func readEffectsEvent(ctx context.Context, d Deps, who sessIdent) (effectsEventJSON, error) {
	if d.Reader == nil {
		return effectsEventJSON{}, errors.New("ui: no reader")
	}
	c := d.Reader.WithContext(ctx)
	if who.name == "" {
		n, err := c.UnassignedConnections()
		if err != nil {
			return effectsEventJSON{}, err
		}
		return effectsEventJSON{Unassigned: &n}, nil
	}
	ident := store.SessionIdent{Principal: who.principal, Client: who.client, Key: who.name}
	ids := []store.SessionIdent{ident}
	vers, err := c.EffectVersions(ids)
	if err != nil {
		return effectsEventJSON{}, err
	}
	counts, err := c.EffectCounts(ids)
	if err != nil {
		return effectsEventJSON{}, err
	}
	v, n := vers[ident], counts[ident]
	return effectsEventJSON{
		Session: who.id(),
		Version: &effectVersionJSON{Count: v.Count, MaxID: v.MaxID},
		Counts:  &effectCountsJSON{Changed: n.Changed, Read: n.Read, Sites: n.Sites},
	}, nil
}
