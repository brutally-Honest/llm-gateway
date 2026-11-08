package effects

import (
	"context"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/hooks"
)

// storeTimeout bounds one effect write.
const storeTimeout = 5 * time.Second

// EffectStore is what read and network effects are saved through.
type EffectStore interface {
	SaveEffects(ctx context.Context, effects []core.Effect, copies [][]byte) error
}

// Rules finds a client's effect rules; nil means the client has none.
type Rules func(client string) core.EffectRules

// Saved is told the session whose effects were stored; an empty key is unassigned.
type Saved func(principal, client, sessionKey string)

// Reads stores a file_read effect for each completed read, search or list tool call the
// client's profile names (spec 014 AC7). Its content is never read from disk: the API
// serves it from the stored hook payload the effect points at.
type Reads struct {
	store   EffectStore
	rules   Rules
	onSaved Saved
	log     *zap.Logger
}

// NewReads returns the read sensor. onSaved may be nil.
func NewReads(st EffectStore, rules Rules, onSaved Saved, log *zap.Logger) *Reads {
	return &Reads{store: st, rules: rules, onSaved: onSaved, log: log}
}

// OnStored is called for each stored hook event; it ignores everything but a completed
// tool call of a session whose client has rules.
func (r *Reads) OnStored(h hooks.HookStored) {
	ev := h.Event
	if ev.Phase != core.PhasePostTool || h.SessionKey == "" || r.rules == nil {
		return
	}
	rules := r.rules(h.Client)
	if rules == nil {
		return
	}
	path, detail, ok := rules.ReadTarget(ev.ToolName, ev.ToolInput)
	if !ok {
		return
	}
	if path == "" {
		path = ev.WorkDir
	} else if !filepath.IsAbs(path) && ev.WorkDir != "" {
		path = filepath.Join(ev.WorkDir, path)
	}
	if path == "" {
		return
	}
	e := core.Effect{
		Kind: core.EffectFileRead, At: h.At, Principal: h.Principal, Client: h.Client,
		SessionKey: h.SessionKey, Path: filepath.Clean(path), Detail: detail, HookID: h.ID,
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	if err := r.store.SaveEffects(ctx, []core.Effect{e}, nil); err != nil {
		r.log.Warn("read effect not stored", zap.String("client", h.Client), zap.Error(err))
		return
	}
	if r.onSaved != nil {
		r.onSaved(h.Principal, h.Client, h.SessionKey)
	}
}
