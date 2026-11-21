// Package netproxy is the network effect sensor of spec 014: an HTTP forward proxy
// (CONNECT tunnels and plain http:// requests) and an optional SOCKS5 proxy, on loopback.
// Bytes are forwarded unchanged and never decrypted; each connection becomes one net_conn
// effect when it ends. It knows no client and no provider.
package netproxy

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// recordQueue is the effects that may wait for the store worker; a full queue drops.
const recordQueue = 1024

// storeTimeout bounds one effect write.
const storeTimeout = 5 * time.Second

// Store is what network effects are saved through.
type Store interface {
	SaveEffects(ctx context.Context, effects []core.Effect, copies [][]byte) error
}

// Linker names the one active session a connection belongs to; false leaves it
// unassigned (AC10).
type Linker func() (store.SessionIdent, bool)

// Saved is told the session whose effects were stored; an empty key is unassigned.
type Saved func(principal, client, sessionKey string)

// unassigned is the identity of a connection linked to no session: the principal the hook
// ingest uses and the unknown client.
var unassigned = store.SessionIdent{Principal: core.PrincipalLocal, Client: "unknown"}

// Recorder queues finished connections and stores them on one worker. Record never
// blocks a tunnel: a full queue drops the effect and counts it.
type Recorder struct {
	store   Store
	link    Linker
	onSaved Saved
	log     *zap.Logger
	own     []ownAddr // set before serving, read only after

	mu     sync.RWMutex
	closed bool
	q      chan core.Effect
	done   <-chan error

	dropped, stored, failed atomic.Int64
}

// NewRecorder starts the store worker. link and onSaved may be nil.
func NewRecorder(st Store, link Linker, onSaved Saved, log *zap.Logger) *Recorder {
	r := &Recorder{store: st, link: link, onSaved: onSaved, log: log, q: make(chan core.Effect, recordQueue)}
	r.done = logging.Go(log, "netproxy_record", r.work)
	return r
}

// Record links e to the active session, or none, and queues it.
func (r *Recorder) Record(e core.Effect) {
	if r.isOwn(e.Host, e.Port) {
		return
	}
	id := unassigned
	if r.link != nil {
		if s, ok := r.link(); ok {
			id = s
		}
	}
	e.Kind = core.EffectNetConn
	e.Principal, e.Client, e.SessionKey = id.Principal, id.Client, id.Key
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		r.dropped.Add(1)
		return
	}
	select {
	case r.q <- e:
	default:
		r.dropped.Add(1)
	}
}

func (r *Recorder) work() error {
	for e := range r.q {
		ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
		err := r.store.SaveEffects(ctx, []core.Effect{e}, nil)
		cancel()
		if err != nil {
			r.failed.Add(1)
			r.log.Warn("network effect not stored", zap.Error(err))
			continue
		}
		r.stored.Add(1)
		if r.onSaved != nil {
			r.onSaved(e.Principal, e.Client, e.SessionKey)
		}
	}
	return nil
}

// Close stops accepting and lets the worker drain the queue until ctx ends.
func (r *Recorder) Close(ctx context.Context) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	close(r.q)
	r.mu.Unlock()
	select {
	case <-r.done:
	case <-ctx.Done():
	}
}

// Counts are the recorder's counters, for the shutdown line.
type Counts struct {
	Stored, Dropped, Failed int64
}

// Counts is a snapshot of the recorder's counters.
func (r *Recorder) Counts() Counts {
	return Counts{Stored: r.stored.Load(), Dropped: r.dropped.Load(), Failed: r.failed.Load()}
}

// SkipTargets names the gateway's own listener addresses (host:port). A connection to one
// of them is still forwarded but is not recorded: when a client's sandbox sends all its
// traffic through the proxy, its own hook calls to the gateway would show as the
// session's network effects (spec 024 AC4). Other loopback targets, a user's own local services, are still
// recorded. "localhost" matches an own loopback address on the same port. Call it before
// the proxy serves.
func (r *Recorder) SkipTargets(addrs ...string) {
	for _, a := range addrs {
		host, port, err := net.SplitHostPort(a)
		if err != nil {
			continue
		}
		p, err := strconv.Atoi(port)
		if err != nil || p == 0 {
			continue
		}
		r.own = append(r.own, ownAddr{host: strings.ToLower(host), port: p})
	}
}

type ownAddr struct {
	host string
	port int
}

// isOwn says whether host:port is one of the gateway's own listeners.
func (r *Recorder) isOwn(host string, port int) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	for _, o := range r.own {
		if o.port != port {
			continue
		}
		if host == o.host || (host == "localhost" && isLoopbackIP(o.host)) || (o.host == "localhost" && isLoopbackIP(host)) {
			return true
		}
	}
	return false
}

func isLoopbackIP(h string) bool {
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
