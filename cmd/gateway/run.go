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
	"github.com/brutally-honest/llm-gateway/internal/logging"
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
}

// registered is every protocol adapter and client profile the gateway runs with, in
// registration order. It is the one list: config's upstream keys and the mounted
// proxies both come from it, so a key and its adapter cannot drift. This package is
// the only one that imports an adapter or a profile.
func registered() ([]core.Adapter, []core.Profile) {
	return []core.Adapter{anthropic.Adapter{}}, []core.Profile{claudecode.Profile{}}
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

	if len(d.args) > 0 && d.args[0] == "dump" {
		return runDump(d)
	}
	if len(d.args) > 0 && d.args[0] == "sessions" {
		return runSessions(d)
	}
	if len(d.args) > 0 && d.args[0] == "timeline" {
		return runTimeline(d)
	}
	if len(d.args) > 0 && d.args[0] == "backfill-sessions" {
		return runBackfill(d)
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

	// The store opens before bind, so a gateway that can't capture never serves
	// (spec 002, Store: Startup).
	var capt *captureRun
	if cfg.Capture.Enabled {
		var code int
		if capt, code = startCapture(cfg.Capture, cfg.UI.Enabled, d, log); capt == nil {
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
	registry := core.NewRegistry(log, opts...)
	for _, p := range profiles {
		registry.AddProfile(p)
	}
	for _, a := range adapters {
		registry.AddAdapter(a, cfg.Upstreams[a.Name()])
	}
	// The adapters' prefixes first, then the test-only routes.
	mounts := append([]func(chi.Router){registry.Mount}, d.mount...)
	srv := server.New(log, mounts...)
	serveErr := logging.Go(log, "serve", func() error { return srv.Serve(ln) })

	var uiSrv *http.Server
	var uiErr <-chan error // nil, so never ready, when the UI is off
	if uiLn != nil {
		uiSrv = &http.Server{
			Handler: ui.Handler(ui.Deps{
				Addr: uiLn.Addr().String(), Log: log, Reader: capt.reader, Hub: capt.hub,
			}),
			ReadHeaderTimeout: 10 * time.Second,
		}
		uiErr = logging.Go(log, "ui_serve", func() error { return uiSrv.Serve(uiLn) })
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

	select {
	case <-ctx.Done():
	case err := <-serveErr:
		log.Error("serve failed", zap.Error(err))
		stopCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		stopUI(stopCtx, uiSrv, capt, log)
		capt.stop(stopCtx, log)
		return exitRuntime
	case err := <-uiErr:
		log.Error("ui serve failed", zap.Error(err))
		stopCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(stopCtx)
		capt.stop(stopCtx, log)
		return exitRuntime
	}

	// ctx is already cancelled, so the shutdown deadline needs a fresh context.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	// The hub closes first so open UI streams end and the UI server can finish; commits
	// from the draining workers then publish into a closed hub, which ignores them.
	uiFailed := !stopUI(shutdownCtx, uiSrv, capt, log)
	// The server shuts down next, so exchanges that end while it waits for in-flight
	// streams are still queued; capture then drains in what is left of the timeout.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			// in_flight counts handlers still running, not open connections.
			log.Error("shutdown timed out", zap.Int64("in_flight", srv.InFlight()))
		} else {
			log.Error("shutdown failed", zap.Error(err))
		}
		_ = srv.Close() // cut what is left; its error adds nothing to the exit code
		capt.stop(shutdownCtx, log)
		return exitRuntime
	}
	capt.stop(shutdownCtx, log)
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

// stopUI closes the hub, then shuts the UI server down. It reports false when the
// shutdown failed or timed out.
func stopUI(ctx context.Context, srv *http.Server, capt *captureRun, log *zap.Logger) bool {
	if capt != nil && capt.hub != nil {
		capt.hub.Close()
	}
	if srv == nil {
		return true
	}
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("ui shutdown failed", zap.Error(err))
		_ = srv.Close()
		return false
	}
	return true
}

// reasonInvalidValue is config's reason for a value it can't use (ADR 0002).
const reasonInvalidValue = "invalid value"

// captureRun is capture while the gateway runs: the store, the sink draining into
// it, and the memory budget the proxies reserve from. A nil *captureRun is capture
// off, and every method is a no-op on it.
type captureRun struct {
	store        capture.Store
	sink         *capture.Sink
	budget       *core.Budget
	maxBodyBytes int64
	// reader and hub are set only when the UI is on.
	reader *store.Reader
	hub    *ui.Hub
}

// hasUI reports whether the UI is on for this run.
func (c *captureRun) hasUI() bool { return c != nil && c.hub != nil }

// startCapture resolves capture.dir, opens the store and starts the sink. On failure
// it logs one line and returns nil and the exit code.
func startCapture(c config.Capture, withUI bool, d deps, log *zap.Logger) (*captureRun, int) {
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
	cfg := capture.Config{QueueSize: c.QueueSize, Workers: c.Workers, DecodeLimit: c.MaxBodyBytes}
	var reader *store.Reader
	var hub *ui.Hub
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
		hub = ui.NewHub(ui.Deps{Log: log, Reader: reader})
		cfg.OnCommit = hub.Publish
	}
	sink := capture.NewSink(cfg, st, log)
	return &captureRun{
		store: st, sink: sink, budget: core.NewBudget(c.MemoryLimit), maxBodyBytes: c.MaxBodyBytes,
		reader: reader, hub: hub,
	}, exitOK
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
	log.Info("capture stopped",
		zap.Int("undrained", undrained),
		zap.Int64("dropped_queue_full", counts.DroppedQueueFull),
		zap.Int64("dropped_memory", c.budget.Dropped()),
		zap.Int64("store_failed", counts.StoreFailed),
		zap.Int64("parse_failed", counts.ParseFailed),
		zap.Int64("memory_peak_bytes", c.budget.Peak()),
	)
}
