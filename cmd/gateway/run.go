package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"io/fs"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/capture"
	"github.com/brutally-honest/llm-gateway/internal/clients/claudecode"
	"github.com/brutally-honest/llm-gateway/internal/config"
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/cost"
	"github.com/brutally-honest/llm-gateway/internal/effects"
	"github.com/brutally-honest/llm-gateway/internal/hooks"
	"github.com/brutally-honest/llm-gateway/internal/install"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/netproxy"
	"github.com/brutally-honest/llm-gateway/internal/observability"
	"github.com/brutally-honest/llm-gateway/internal/protocols/anthropic"
	"github.com/brutally-honest/llm-gateway/internal/ratelimit"
	"github.com/brutally-honest/llm-gateway/internal/server"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/ui"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// deps is everything run takes from the outside world, so tests can swap it.
type deps struct {
	args      []string
	lookupEnv func(string) (string, bool)
	stdout    io.Writer
	listen    func(network, addr string) (net.Listener, error)
	mount     []func(chi.Router) // test-only routes; nil in main
	// openStore opens the capture store in dir; nil means store.Open. Tests swap it
	// to hold up or break the store.
	openStore func(dir string, log *zap.Logger) (capture.Store, error)
	// stdin is what the install commands read answers from; installEnv replaces the real
	// machine in their tests (spec 023). Both nil for the gateway itself.
	stdin      io.Reader
	installEnv *install.Env
}

// registered is every protocol adapter and client profile the gateway runs with, in
// registration order. It is the one list: config's upstream keys and the mounted
// proxies both come from it, so a key and its adapter cannot drift. This package is
// the only one that imports an adapter or a profile.
func registered() ([]core.Adapter, []core.Profile) {
	return []core.Adapter{anthropic.Adapter{}}, []core.Profile{claudecode.Profile{}}
}

// costBooker is an adapter that prices its calls: its protocol's published price table
// and how to read its usage detail (spec 020).
type costBooker interface {
	CostBook() cost.Book
}

// registerCostBooks registers the price book of every adapter that has one, under the
// adapter's protocol name, before any view is built (research D9). An adapter with no
// book leaves its calls "no price".
func registerCostBooks() {
	adapters, _ := registered()
	for _, a := range adapters {
		if b, ok := a.(costBooker); ok {
			cost.Register(a.Name(), b.CostBook())
		}
	}
}

// upstreamSpecs is the upstreams.<name> block config expects for each adapter.
func upstreamSpecs(adapters []core.Adapter) []config.UpstreamSpec {
	specs := make([]config.UpstreamSpec, 0, len(adapters))
	for _, a := range adapters {
		specs = append(specs, config.UpstreamSpec{Name: a.Name(), DefaultBaseURL: a.DefaultBaseURL()})
	}
	return specs
}

// Exit codes.
const (
	exitOK      = 0
	exitRuntime = 1 // bind, serve or shutdown failed
	exitConfig  = 2 // bad flags or invalid config; nothing was bound
)

// loadConfig loads config from path, the default file and the environment, and logs
// one line on boot when it is invalid.
func loadConfig(boot *zap.Logger, path string, lookupEnv func(string) (string, bool),
	adapters []core.Adapter) (config.Config, config.Source, bool) {
	cfg, src, err := config.Load(config.Options{Path: path, DefaultPath: "config.yaml", LookupEnv: lookupEnv, Upstreams: upstreamSpecs(adapters)})
	if err != nil {
		var ce *config.Error
		if errors.As(err, &ce) {
			// Built from the error's fields, never err.Error() of a parser (ADR 0002).
			fields := []zap.Field{
				zap.String("key", ce.Key),
				zap.String("source", ce.Source),
				zap.String("reason", ce.Reason),
			}
			if ce.Line > 0 {
				fields = append(fields, zap.Int("line", ce.Line))
			}
			boot.Error("invalid config", fields...)
		} else {
			boot.Error("invalid config")
		}
		return config.Config{}, config.Source{}, false
	}
	return cfg, src, true
}

// captureDir is capture.dir, or the default location when it is unset. With neither
// it logs one invalid config line and returns false.
func captureDir(c config.Capture, lookupEnv func(string) (string, bool), log *zap.Logger) (string, bool) {
	if c.Dir != "" {
		return c.Dir, true
	}
	dir, ok := config.DefaultCaptureDir(lookupEnv)
	if !ok {
		// Neither an absolute XDG_DATA_HOME nor an absolute HOME: no default.
		log.Error("invalid config",
			zap.String("key", "capture.dir"),
			zap.String("source", "default"),
			zap.String("reason", reasonInvalidValue))
	}
	return dir, ok
}

// run starts the gateway and serves until ctx is cancelled. It returns the exit code.
func run(ctx context.Context, d deps) int {
	boot := logging.Bootstrap(d.stdout)
	registerCostBooks()

	if len(d.args) > 0 && d.args[0] == "dump" {
		return runDump(d)
	}
	if len(d.args) > 0 && d.args[0] == "sessions" {
		return runSessions(d)
	}
	if len(d.args) > 0 && d.args[0] == "timeline" {
		return runTimeline(d)
	}
	if len(d.args) > 0 && d.args[0] == "hooks" {
		return runHooks(d)
	}
	if len(d.args) > 0 && d.args[0] == "backfill-sessions" {
		return runBackfill(d)
	}
	if isHelp(d.args) {
		return runHelp(d)
	}
	if len(d.args) > 0 {
		switch d.args[0] {
		case "install":
			return runInstall(d)
		case "uninstall":
			return runUninstall(d)
		case "doctor":
			return runDoctor(d)
		}
	}

	fs := flag.NewFlagSet("gateway", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // the flag package never prints usage text
	configPath := fs.String("config", "", "path to the config file (default ./config.yaml if present)")
	switch err := fs.Parse(d.args); {
	case errors.Is(err, flag.ErrHelp):
		// -h or -help: usage as one JSON line; nothing is loaded or bound.
		boot.Info("usage", zap.Strings("flags", []string{"-config <path>"}))
		return exitOK
	case err != nil:
		// The flag package's message can quote the value, so only the fact is logged.
		boot.Error("invalid flags")
		return exitConfig
	case fs.NArg() > 0:
		// The gateway takes no arguments; -config is the only way to name a file. A
		// positional one can be anything, a path included, so only the count is logged.
		boot.Error("invalid flags", zap.String("reason", "unexpected argument"), zap.Int("count", fs.NArg()))
		return exitConfig
	}

	adapters, profiles := registered()
	cfg, src, ok := loadConfig(boot, *configPath, d.lookupEnv, adapters)
	if !ok {
		return exitConfig
	}

	log := logging.New(d.stdout, cfg.LogLevel)
	protocols := make([]string, 0, len(adapters))
	for _, a := range adapters {
		protocols = append(protocols, a.Name())
	}
	metrics := observability.NewMetrics(version, protocols...)
	var tracing *observability.Tracing // nil: tracing off (spec 021 AC12)
	if cfg.Tracing.Enabled {
		var err error
		if tracing, err = observability.StartTracing(cfg.Tracing.OTLPEndpoint, version, log); err != nil {
			log.Error("invalid config", zap.String("key", "tracing.otlp_endpoint"), zap.String("reason", reasonInvalidValue))
			return exitConfig
		}
		// Deferred first, so it runs last: after capture has drained and ended the
		// capture spans, the spans still queued get a short flush.
		defer func() {
			flushCtx, cancel := context.WithTimeout(context.Background(), traceFlushTimeout)
			defer cancel()
			if err := tracing.Shutdown(flushCtx); err != nil {
				log.Warn("trace flush failed", zap.Error(err))
			}
		}()
		log.Info("tracing on", zap.String("otlp_endpoint", cfg.Tracing.OTLPEndpoint.String()))
	}

	// The store opens before bind, so a gateway that can't capture never serves
	// (spec 002, Store: Startup).
	var capt *captureRun
	if cfg.Capture.Enabled {
		var code int
		if capt, code = startCaptureWith(ctx, cfg.Capture, cfg.Hooks, cfg.UI.Enabled, cfg.Effects, profiles, d, log, metrics); capt == nil {
			return code
		}
	} else if cfg.UI.Enabled {
		log.Warn("ui is off because capture is disabled", zap.String("key", "capture.enabled"))
	}

	ln, err := d.listen("tcp", cfg.ListenAddr)
	if err != nil {
		// Nothing was served, so nothing was queued: the store just closes.
		_, _ = capt.close(context.Background())
		// The address is valid (config checked it), so this is the OS refusing. The
		// error names the address, so only a fixed reason is logged.
		reason := "bind failed"
		if errors.Is(err, syscall.EADDRINUSE) {
			reason = "address in use"
		}
		log.Error("cannot bind", zap.String("key", "listen_addr"), zap.String("reason", reason))
		return exitRuntime
	}

	opts := capt.options()
	if cfg.RateLimit.Enabled {
		limiter, err := ratelimit.New(cfg.RateLimit)
		if err != nil {
			_, _ = capt.close(context.Background())
			_ = ln.Close()
			log.Error("invalid config", zap.String("key", "ratelimit.strategy"), zap.String("reason", reasonInvalidValue))
			return exitConfig
		}
		opts = append(opts, core.WithGuard(func(a core.Adapter) func(http.Handler) http.Handler {
			return ratelimit.Middleware(limiter, a, log)
		}))
	}
	// The UI binds before anything serves, so a taken port stops the proxy too (E17).
	var uiLn net.Listener
	if capt.hasUI() {
		uiLn, err = d.listen("tcp", cfg.UI.ListenAddr)
		if err != nil {
			_, _ = capt.close(context.Background())
			_ = ln.Close()
			reason := "bind failed"
			if errors.Is(err, syscall.EADDRINUSE) {
				reason = "address in use"
			}
			log.Error("cannot bind", zap.String("key", "ui.listen_addr"), zap.String("reason", reason))
			return exitRuntime
		}
	}
	// The proxy listeners bind before anything serves too, so a taken port stops the
	// gateway with one line (spec 014, E14).
	if code := capt.startProxy(cfg.Effects, d.listen, log); code != exitOK {
		_, _ = capt.close(context.Background())
		_ = ln.Close()
		if uiLn != nil {
			_ = uiLn.Close()
		}
		return code
	}
	// The hook route is always mounted; with capture off it answers and counts only.
	hookSink, hookOn := capt.hookSink(), true
	stopHooks := func() {} // capture on: the sink stops with capture
	if hookSink == nil {
		hookOn = false
		hookSink = hooks.NewSink(hookConfig(cfg.Hooks, profiles), nil, log)
		// No "capture stopped" line is written with capture off, so the hook counters
		// (capture_off above all) get a line of their own.
		stopHooks = func() {
			hookSink.Close(context.Background())
			hc := hookSink.Counts()
			log.Info("hook ingest stopped",
				zap.Int64("hooks_queue_full", hc.QueueFull),
				zap.Int64("hooks_oversize", hc.Oversize),
				zap.Int64("hooks_capture_off", hc.CaptureOff),
				zap.Int64("hooks_not_json", hc.NotJSON),
				zap.Int64("hooks_store_failed", hc.StoreFailed),
			)
		}
		defer hookSink.Close(context.Background()) // the early returns; Close is idempotent
	}
	hookHandler := hooks.NewHandler(hookSink, log, cfg.Capture.MaxBodyBytes, hookOn)
	if t := tracing.Tracer(); t != nil {
		opts = append(opts, core.WithTracer(t))
	}
	if capt.proxyOn() {
		// Upstream calls never loop back through the gateway's own proxy (spec 014 AC11).
		opts = append(opts, core.WithOwnProxies(cfg.Effects.Network.HTTPProxyAddr, cfg.Effects.Network.SocksProxyAddr))
	}
	registry := core.NewRegistry(log, opts...)
	for _, p := range profiles {
		registry.AddProfile(p)
	}
	for _, a := range adapters {
		registry.AddAdapter(a, cfg.Upstreams[a.Name()])
	}
	// The adapters' prefixes first, then the test-only routes.
	mounts := append([]func(chi.Router){registry.Mount, hookHandler.Mount}, d.mount...)
	capt.observe(metrics, log)
	srv := server.NewWithMetrics(log, metrics, mounts...)
	serveErr := logging.Go(log, "serve", func() error { return srv.Serve(ln) })

	var uiSrv *http.Server
	var uiErr <-chan error // nil, so never ready, when the UI is off
	if uiLn != nil {
		deps := capt.uiDeps(ctx, uiLn.Addr().String(), log)
		uiSrv = &http.Server{
			Handler:           ui.Handler(deps),
			ReadHeaderTimeout: 10 * time.Second,
		}
		srvUI := uiSrv
		// Spec 019: the sessions list is built before the UI accepts, so the first page
		// load after a restart answers instead of timing out on a cold build; a page that
		// connects meanwhile waits in the listen queue. The proxy serves from the start.
		uiErr = logging.Go(log, "ui_serve", func() error {
			start := time.Now()
			if err := ui.Warm(ctx, deps); err != nil && ctx.Err() == nil {
				log.Warn("ui warm-up failed; the list builds on first use", zap.Error(err))
			}
			warm := time.Since(start)
			metrics.Warmup(warm)
			log.Info("ui ready", zap.Int64("warm_ms", warm.Milliseconds()))
			return srvUI.Serve(uiLn)
		})
		log.Info("ui started", zap.String("addr", uiLn.Addr().String()))
	}

	configSource := src.File
	if configSource == "" {
		configSource = "defaults"
	}
	log.Info("gateway started",
		zap.String("version", version),
		zap.String("addr", ln.Addr().String()),
		zap.String("config_source", configSource),
		zap.Strings("env_overrides", src.EnvOverrides),
	)

wait:
	for {
		select {
		case <-ctx.Done():
			break wait
		case err := <-serveErr:
			log.Error("serve failed", zap.Error(err))
			stopCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
			defer cancel()
			stopUI(stopCtx, uiSrv, capt, log)
			capt.stop(stopCtx, log)
			stopHooks()
			return exitRuntime
		case err := <-uiErr:
			// The UI never fails proxying or capture: only the UI stops. Closing the hub
			// makes later commits a no-op; the reader closes with capture at shutdown.
			log.Error("ui serve failed; the ui is off, the proxy keeps running", zap.Error(err))
			if capt != nil && capt.hub != nil {
				capt.hub.Close()
			}
			_ = uiSrv.Close()
			uiSrv, uiErr = nil, nil
		}
	}

	// Spec 019: nothing new is accepted, and what never ends by itself ends now: the
	// proxy's tunnels are cut (stored as closed early) and the UI's live streams close.
	// Only in-flight model calls get the grace period (shutdown_timeout).
	log.Info("shutting down", zap.Duration("grace", cfg.ShutdownTimeout))
	stopStart := time.Now()
	tunnelsCut := capt.openTunnels()
	// ctx is already cancelled, so the grace period needs a fresh context. The server
	// stops accepting first, and shuts down before capture, so exchanges that end while
	// it waits for in-flight streams are still queued.
	graceCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	apiDone := logging.Go(log, "api_shutdown", func() error { return srv.Shutdown(graceCtx) })
	capt.cutProxy()
	uiCtx, uiCancel := context.WithTimeout(context.Background(), uiStopTimeout)
	uiFailed := !stopUI(uiCtx, uiSrv, capt, log)
	uiCancel()
	if err := <-apiDone; err != nil {
		// The calls still running now are the ones the stop cuts (spec 021 AC7).
		callsCut := srv.InFlight()
		if errors.Is(err, context.DeadlineExceeded) {
			// in_flight counts handlers still running, not open connections.
			log.Error("shutdown timed out", zap.Int64("in_flight", callsCut))
		} else {
			log.Error("shutdown failed", zap.Error(err))
		}
		// The calls still running are cut with a cause, so each is captured as cut by
		// the gateway (gateway_error gateway_shutdown); their handlers get a moment to
		// hand the capture over before the connections close.
		srv.Cancel(core.ErrShutdown)
		cutCtx, cutCancel := context.WithTimeout(context.Background(), cutWait)
		srv.WaitIdle(cutCtx)
		cutCancel()
		_ = srv.Close() // cut what is left; its error adds nothing to the exit code
		drainCtx, drainCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer drainCancel()
		capt.stop(drainCtx, log)
		stopHooks()
		capt.recordShutdown(stopStart, callsCut, tunnelsCut, log)
		return exitRuntime
	}
	// Capture drains on a budget of its own, as long again as the grace period, never
	// what the grace period left over.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer drainCancel()
	capt.stop(drainCtx, log)
	stopHooks()
	capt.recordShutdown(stopStart, 0, tunnelsCut, log)
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("serve failed", zap.Error(err))
		return exitRuntime
	}
	if uiFailed {
		return exitRuntime
	}
	log.Info("gateway stopped")
	return exitOK
}

// stopUI closes the hub, so open live streams end, then shuts the UI server down,
// cutting what still runs when ctx ends. It reports false when the shutdown failed.
func stopUI(ctx context.Context, srv *http.Server, capt *captureRun, log *zap.Logger) bool {
	if capt != nil && capt.hub != nil {
		capt.hub.Close()
	}
	if srv == nil {
		return true
	}
	if err := srv.Shutdown(ctx); err != nil {
		_ = srv.Close()
		if errors.Is(err, context.DeadlineExceeded) {
			// A UI read still running when the stop's budget ends is cut: the UI only
			// reads, so nothing is lost (spec 019).
			log.Warn("ui requests cut at shutdown")
			return true
		}
		log.Error("ui shutdown failed", zap.Error(err))
		return false
	}
	return true
}

// The shutdown budgets besides the grace period (spec 019, research D-entries): the UI
// server's stop once its streams are closed, and the wait for cut calls' handlers to
// hand their capture over. Capture's drain gets a fresh shutdown_timeout of its own.
const (
	uiStopTimeout = 2 * time.Second
	cutWait       = 2 * time.Second
	// traceFlushTimeout bounds the last span export at exit (spec 021): a collector that
	// is down must not hold the stop open.
	traceFlushTimeout = 2 * time.Second
)

// reasonInvalidValue is config's reason for a value it can't use (ADR 0002).
const reasonInvalidValue = "invalid value"

// captureRun is capture while the gateway runs: the store, the sink draining into
// it, and the memory budget the proxies reserve from. A nil *captureRun is capture
// off, and every method is a no-op on it.
type captureRun struct {
	store        capture.Store
	sink         *capture.Sink
	hooks        *hooks.Sink // nil when the store cannot save hook events
	budget       *core.Budget
	maxBodyBytes int64
	// reader and hub are set only when the UI is on.
	reader *store.Reader
	hub    *ui.Hub
	// timelines is the one timeline cache the hub and the UI handler share, so the
	// process holds a single set of builds under a single byte budget.
	timelines *ui.TimelineCache
	views     *ui.ViewCache
	// digests is the one digest cache they share, for the same reason.
	digests *ui.DigestCache
	// effects is the effect sensors; nil when effects are off (spec 014, E15).
	effects *effectsRun
	// skew is effects.attribution.skew, for labelling effects in the UI (spec 015).
	skew time.Duration
	// dir is the capture directory, where a stop leaves its record (spec 021).
	dir string
	// metrics is the gateway's own metrics; nil in tests that leave them out.
	metrics *observability.Metrics
}

// hookSink is the hook sink, or nil with capture off or a store that cannot save hooks.
func (c *captureRun) hookSink() *hooks.Sink {
	if c == nil {
		return nil
	}
	return c.hooks
}

// hookConfig sizes the hook sink and finds a client's mapper through its profile.
func hookConfig(h config.Hooks, profiles []core.Profile) hooks.Config {
	return hooks.Config{
		QueueSize: h.QueueSize, Workers: h.Workers, MemoryLimit: h.MemoryLimit,
		Mappers: func(client string) core.HarnessMapper {
			for _, p := range profiles {
				if p.Name() == client {
					if m, ok := p.(core.HarnessMapper); ok {
						return m
					}
				}
			}
			return nil
		},
	}
}

// hasUI reports whether the UI is on for this run.
func (c *captureRun) hasUI() bool { return c != nil && c.hub != nil }

// uiDeps is what the UI handler is built from.
func (c *captureRun) uiDeps(ctx context.Context, addr string, log *zap.Logger) ui.Deps {
	return ui.Deps{
		Addr: addr, Log: log, Reader: c.reader, Hub: c.hub,
		Rules: uiRules(), Ctx: ctx, Timelines: c.timelines, Digests: c.digests, Views: c.views,
		AttributionSkew: c.skew, Metrics: c.metrics,
	}
}

// startCapture resolves capture.dir, opens the store and starts the sink. On failure
// it logs one line and returns nil and the exit code.
func startCapture(ctx context.Context, c config.Capture, h config.Hooks, withUI bool, profiles []core.Profile, d deps, log *zap.Logger) (*captureRun, int) {
	return startCaptureWith(ctx, c, h, withUI, config.Effects{}, profiles, d, log, nil)
}

// startCaptureWith is startCapture with the effect sensors of e hanging off the hook sink.
func startCaptureWith(ctx context.Context, c config.Capture, h config.Hooks, withUI bool, e config.Effects, profiles []core.Profile, d deps, log *zap.Logger, m *observability.Metrics) (*captureRun, int) {
	dir, ok := captureDir(c, d.lookupEnv, log)
	if !ok {
		return nil, exitConfig
	}
	open := d.openStore
	if open == nil {
		open = func(dir string, log *zap.Logger) (capture.Store, error) { return store.Open(dir, log) }
	}
	st, err := open(dir, log)
	if err != nil {
		// The error can carry driver text, so only the path and a fixed reason are
		// logged.
		log.Error("cannot open store",
			zap.String("key", "capture.dir"),
			zap.String("path", dir),
			zap.String("reason", storeOpenReason(err)))
		return nil, exitRuntime
	}
	cfg := capture.Config{QueueSize: c.QueueSize, Workers: c.Workers, DecodeLimit: c.MaxBodyBytes, Metrics: m}
	var reader *store.Reader
	var hub *ui.Hub
	var timelines *ui.TimelineCache
	var digests *ui.DigestCache
	var views *ui.ViewCache
	if withUI {
		// The store exists now (E2), so the read-only view can open on it.
		if reader, err = store.OpenReader(dir); err != nil {
			_ = st.Close()
			log.Error("cannot open store reader",
				zap.String("key", "capture.dir"),
				zap.String("path", dir),
				zap.String("reason", storeOpenReason(err)))
			return nil, exitRuntime
		}
		timelines = ui.NewTimelineCache(ctx, log)
		digests = ui.NewDigestCache(ctx, log)
		views = ui.NewViewCache()
		hub = ui.NewHub(ui.Deps{Log: log, Reader: reader, Rules: uiRules(), Timelines: timelines, Digests: digests,
			Views: views, AttributionSkew: e.Attribution.Skew, Metrics: m})
		cfg.OnCommit = hub.Publish
	}
	sink := capture.NewSink(cfg, st, log)
	var hookSink *hooks.Sink
	var eff *effectsRun
	if hs, ok := st.(hooks.Store); ok {
		hcfg := hookConfig(h, profiles)
		var onStored []func(hooks.HookStored)
		if hub != nil {
			// A stored hook event reaches the open web UI streams (AC26).
			onStored = append(onStored, func(s hooks.HookStored) { hub.PublishHook(s.Principal, s.Client, s.SessionKey) })
		}
		eff = startEffects(e, st, hub, profiles, log)
		onStored = append(onStored, eff.callbacks()...)
		if len(onStored) > 0 {
			hcfg.OnStored = func(s hooks.HookStored) {
				for _, f := range onStored {
					f(s)
				}
			}
		}
		hookSink = hooks.NewSink(hcfg, hs, log)
	}
	return &captureRun{
		store: st, sink: sink, hooks: hookSink, budget: core.NewBudget(c.MemoryLimit), maxBodyBytes: c.MaxBodyBytes,
		reader: reader, hub: hub, timelines: timelines, views: views, digests: digests, effects: eff,
		skew: e.Attribution.Skew, dir: dir, metrics: m,
	}, exitOK
}

// effectsStore is what the effect sensors save through.
type effectsStore interface {
	effects.EffectStore
	effects.CheckpointStore
}

// effectsRun is the effect sensors while the gateway runs. A nil *effectsRun is effects
// off, and every method is a no-op on it.
type effectsRun struct {
	reads  *effects.Reads
	runner *effects.Runner
	saved  effects.Saved
	store  effectsStore
	// proxy and rec are set once the proxy listeners are bound; nil with the network
	// sensor off.
	proxy *netproxy.Proxy
	rec   *netproxy.Recorder
}

// startEffects starts the effect sensors that hang off stored hook events (spec 014); nil
// when effects are off (E15) or the store cannot hold them.
func startEffects(e config.Effects, st capture.Store, hub *ui.Hub, profiles []core.Profile, log *zap.Logger) *effectsRun {
	es, ok := st.(effectsStore)
	if !e.Enabled || !ok {
		return nil
	}
	saved := func(principal, client, key string) {
		if hub != nil {
			hub.PublishEffects(principal, client, key)
		}
	}
	rules := effectRules(profiles)
	run := &effectsRun{
		reads:  effects.NewReads(es, rules, saved, log),
		runner: effects.NewRunner(e, es, rules, saved, log),
		saved:  saved,
		store:  es,
	}
	run.runner.Start()
	return run
}

// callbacks are the sensors' hook callbacks.
func (e *effectsRun) callbacks() []func(hooks.HookStored) {
	if e == nil {
		return nil
	}
	return []func(hooks.HookStored){e.reads.OnStored, e.runner.OnStored}
}

// startProxy binds the HTTP proxy listener and, when set, the SOCKS5 one, then serves
// them; it links each connection to the runner's one active session (AC10). With effects
// or the network sensor off it does nothing. A bind failure logs one line and closes
// what was bound.
func (c *captureRun) startProxy(e config.Effects, listen func(network, addr string) (net.Listener, error), log *zap.Logger) int {
	if c == nil || c.effects == nil || !e.Network.Enabled {
		return exitOK
	}
	type bound struct {
		key string
		ln  net.Listener
	}
	var lns []bound
	for _, l := range []struct{ key, addr string }{
		{"effects.network.http_proxy_addr", e.Network.HTTPProxyAddr},
		{"effects.network.socks_proxy_addr", e.Network.SocksProxyAddr},
	} {
		if l.addr == "" {
			continue
		}
		ln, err := listen("tcp", l.addr)
		if err != nil {
			for _, b := range lns {
				_ = b.ln.Close()
			}
			reason := "bind failed"
			if errors.Is(err, syscall.EADDRINUSE) {
				reason = "address in use"
			}
			log.Error("cannot bind", zap.String("key", l.key), zap.String("reason", reason))
			return exitRuntime
		}
		lns = append(lns, bound{l.key, ln})
	}
	run := c.effects
	run.rec = netproxy.NewRecorder(run.store, run.runner.ActiveSession, netproxy.Saved(run.saved), log)
	run.proxy = netproxy.New(run.rec, log)
	for _, b := range lns {
		if b.key == "effects.network.http_proxy_addr" {
			run.proxy.ListenHTTP(b.ln)
		} else {
			run.proxy.ListenSOCKS(b.ln)
		}
		log.Info("proxy listener started", zap.String("key", b.key), zap.String("addr", b.ln.Addr().String()))
	}
	return exitOK
}

// cut closes the proxy listeners and cuts every open tunnel at once, stored as closed
// early (spec 019): a tunnel never ends by itself, so waiting for it only delays the stop.
func (e *effectsRun) cut() {
	if e == nil || e.proxy == nil {
		return
	}
	now, cancel := context.WithCancel(context.Background())
	cancel()
	e.proxy.Shutdown(now)
}

// cutProxy is cut on the capture's effects; a no-op with capture or the proxy off.
func (c *captureRun) cutProxy() {
	if c == nil {
		return
	}
	c.effects.cut()
}

// close stops the proxy listeners (open tunnels are stored as closed early once ctx
// ends), drains the network recorder, then stops the checkpoint runner.
func (e *effectsRun) close(ctx context.Context) {
	if e == nil {
		return
	}
	if e.proxy != nil {
		e.proxy.Shutdown(ctx)
		e.rec.Close(ctx)
	}
	e.runner.Close(ctx)
}

// observe registers the metrics read from capture's parts when scraped (spec 021): the
// queue, the drops, the open tunnels and UI streams, and the previous stop's record.
// With capture off there are none (E1).
func (c *captureRun) observe(m *observability.Metrics, log *zap.Logger) {
	if c == nil || m == nil {
		return
	}
	m.GaugeFunc("gateway_capture_queue_depth", "Exchanges waiting in the capture queue.",
		func() float64 { return float64(c.sink.QueueDepth()) })
	m.GaugeFunc("gateway_capture_queue_capacity", "The capture queue's size, capture.queue_size.",
		func() float64 { return float64(c.sink.QueueCapacity()) })
	m.CounterFunc("gateway_capture_dropped_total", "Exchanges dropped from capture, by reason.",
		map[string]string{"reason": "queue_full"}, func() float64 { return float64(c.sink.Counts().DroppedQueueFull) })
	m.CounterFunc("gateway_capture_dropped_total", "Exchanges dropped from capture, by reason.",
		map[string]string{"reason": "memory"}, func() float64 { return float64(c.budget.Dropped()) })
	if c.proxyOn() {
		proxy := c.effects.proxy
		m.GaugeFunc("gateway_proxy_tunnels_open", "Open tunnels and forwarded requests on the proxy listeners.",
			func() float64 { return float64(proxy.Open()) })
	}
	if c.hub != nil {
		m.GaugeFunc("gateway_ui_streams_open", "Open UI live streams (SSE).",
			func() float64 { return float64(c.hub.Streams()) })
	}
	switch last, ok, err := observability.LoadLastShutdown(c.dir); {
	case err != nil:
		log.Warn("last shutdown record unreadable; its metrics are off", zap.String("path", c.dir))
	case ok:
		m.LastShutdown(last)
	}
}

// openTunnels is how many proxy tunnels are open now; 0 with the proxy off.
func (c *captureRun) openTunnels() int64 {
	if !c.proxyOn() {
		return 0
	}
	return int64(c.effects.proxy.Open())
}

// recordShutdown leaves this stop's record in the capture directory, for the next
// start's metrics (spec 021 AC7). A failure costs only those metrics.
func (c *captureRun) recordShutdown(start time.Time, callsCut, tunnelsCut int64, log *zap.Logger) {
	if c == nil {
		return
	}
	now := time.Now()
	rec := observability.LastShutdown{At: now, Duration: now.Sub(start).Seconds(), CallsCut: callsCut, TunnelsCut: tunnelsCut}
	if err := observability.SaveLastShutdown(c.dir, rec); err != nil {
		log.Warn("cannot save the shutdown record", zap.String("path", c.dir))
	}
}

// proxyOn reports whether the gateway's own proxy listeners are serving.
func (c *captureRun) proxyOn() bool { return c != nil && c.effects != nil && c.effects.proxy != nil }

// proxyCounts are the network recorder's counters, zero with the sensor off.
func (e *effectsRun) proxyCounts() netproxy.Counts {
	if e == nil || e.rec == nil {
		return netproxy.Counts{}
	}
	return e.rec.Counts()
}

// counts are the sensors' counters for the shutdown line.
func (e *effectsRun) counts() effects.Counts {
	if e == nil {
		return effects.Counts{}
	}
	return e.runner.Counts()
}

// effectRules finds a client's effect rules through its profile.
func effectRules(profiles []core.Profile) effects.Rules {
	return func(client string) core.EffectRules {
		for _, p := range profiles {
			if p.Name() == client {
				if r, ok := p.(core.EffectRules); ok {
					return r
				}
			}
		}
		return nil
	}
}

// storeOpenReason is a fixed reason for a failed store open.
func storeOpenReason(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	case errors.Is(err, syscall.EROFS):
		return "read-only file system"
	case errors.Is(err, syscall.ENOSPC):
		return "no space left on device"
	case errors.Is(err, syscall.ENOTDIR):
		return "not a directory"
	default:
		return "cannot open or migrate"
	}
}

// options turns capture on in the registry; with capture off there are none.
func (c *captureRun) options() []core.RegistryOption {
	if c == nil {
		return nil
	}
	return []core.RegistryOption{core.WithCapture(core.Capture{
		Sink:         c.sink,
		Budget:       c.budget,
		MaxBodyBytes: c.maxBodyBytes,
		Principal:    core.LocalPrincipal{},
	})}
}

// close stops the sink, then closes the store, and returns how many exchanges were
// left undrained when ctx ran out.
func (c *captureRun) close(ctx context.Context) (undrained int, err error) {
	if c == nil {
		return 0, nil
	}
	if c.hub != nil {
		c.hub.Close()
	}
	if c.hooks != nil {
		c.hooks.Close(ctx)
	}
	// After the hook sink: its last events may still signal a checkpoint.
	c.effects.close(ctx)
	undrained = c.sink.Close(ctx)
	err = c.store.Close()
	if c.reader != nil {
		_ = c.reader.Close()
	}
	return undrained, err
}

// stop is close plus the one capture stopped line (spec 002, Shutdown): what was left
// undrained, every drop and failure count, and the budget's peak (research Q12).
func (c *captureRun) stop(ctx context.Context, log *zap.Logger) {
	if c == nil {
		return
	}
	undrained, err := c.close(ctx)
	if err != nil {
		// Every write had finished, so nothing is lost; the error names the path.
		log.Warn("store close failed", zap.Error(err))
	}
	counts := c.sink.Counts()
	var hc hooks.Counts
	if c.hooks != nil {
		hc = c.hooks.Counts()
	}
	ec, pc := c.effects.counts(), c.effects.proxyCounts()
	log.Info("capture stopped",
		zap.Int64("effects_checkpoints", ec.Checkpoints),
		zap.Int64("effects_stored", ec.EffectsStored+pc.Stored),
		zap.Int64("effects_git_failures", ec.GitFailures),
		zap.Int64("effects_proxy_dropped", pc.Dropped),
		zap.Int64("effects_proxy_store_failed", pc.Failed),
		zap.Int64("hooks_queue_full", hc.QueueFull),
		zap.Int64("hooks_oversize", hc.Oversize),
		zap.Int64("hooks_capture_off", hc.CaptureOff),
		zap.Int64("hooks_not_json", hc.NotJSON),
		zap.Int64("hooks_store_failed", hc.StoreFailed),
		zap.Int("undrained", undrained),
		zap.Int64("dropped_queue_full", counts.DroppedQueueFull),
		zap.Int64("dropped_memory", c.budget.Dropped()),
		zap.Int64("store_failed", counts.StoreFailed),
		zap.Int64("parse_failed", counts.ParseFailed),
		zap.Int64("memory_peak_bytes", c.budget.Peak()),
	)
}

// uiRules maps each client with UI rules to them.
func uiRules() map[string]ui.ClientRules {
	return map[string]ui.ClientRules{claudecode.Name: claudecode.Profile{}}
}
