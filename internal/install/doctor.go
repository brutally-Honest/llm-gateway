package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/config"
	"github.com/brutally-honest/llm-gateway/internal/startup"
)

// DocsURL is docs/install.md on GitHub; doctor links each check to its section there.
const DocsURL = "https://github.com/brutally-Honest/llm-gateway/blob/main/docs/install.md"

// Level is a check's result.
type Level string

// The three results (spec AC21).
const (
	Pass Level = "pass"
	Warn Level = "warn"
	Fail Level = "fail"
)

// Result is one doctor check.
type Result struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Level  Level  `json:"level"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
	Link   string `json:"link"`
}

// DoctorOptions are `gateway doctor`'s flags.
type DoctorOptions struct {
	JSON bool
}

// anchor is the docs/install.md section of a check: "### Check: <id>".
func anchor(id string) string { return DocsURL + "#check-" + id }

// Doctor runs `gateway doctor` and returns its exit code: non-zero on any fail (AC21).
func Doctor(e *Env, c Client, o DoctorOptions) int {
	results := Diagnose(e, c)
	failed := false
	for _, r := range results {
		failed = failed || r.Level == Fail
	}
	if o.JSON {
		out, _ := json.MarshalIndent(struct {
			OK     bool     `json:"ok"`
			Checks []Result `json:"checks"`
		}{!failed, results}, "", "  ")
		e.printf("%s\n", out)
	} else {
		width := 0
		for _, r := range results {
			width = max(width, len(r.Name))
		}
		for _, r := range results {
			e.printf("%-4s  %-*s  %s\n", r.Level, width, r.Name, r.Detail)
			if r.Level != Pass {
				e.printf("      %-*s  fix: %s\n      %-*s  %s\n", width, "", r.Fix, width, "", r.Link)
			}
		}
		if failed {
			e.printf("\nSome checks failed.\n")
		} else {
			e.printf("\nAll required checks pass.\n")
		}
	}
	if failed {
		return ExitStopped
	}
	return ExitOK
}

// Diagnose is every check: the preflight's (with no offers) and the live install's (AC22).
func Diagnose(e *Env, c Client) []Result {
	p := paths(e, c)
	st, stErr := readState(p.State)
	noService := st != nil && st.Service == nil
	pre := runPreflight(e, c, checkOpts{noService: noService})
	var out []Result
	add := func(id, name string, lvl Level, detail, fix string) {
		out = append(out, Result{ID: id, Name: name, Level: lvl, Detail: detail, Fix: fix, Link: anchor(id)})
	}

	for _, d := range pre.Deps {
		if d.ID == "ports" {
			continue // the listeners check below covers a running gateway's ports
		}
		lvl := Pass
		switch {
		case d.Skipped:
			lvl = Warn
		case !d.Found && d.Required:
			lvl = Fail
		case !d.Found:
			lvl = Warn
		}
		fix := d.Manual
		if fix == "" && d.Pkg != "" {
			if cmd := pkgCommand(pre.PkgMgr, d.Pkg, e.Root); cmd != "" {
				fix = cmd + " (or run gateway install --install-deps)"
			} else {
				fix = manualPkgSteps(e.GOOS, d.Pkg)
			}
		}
		if d.Skipped {
			fix = "run gateway install without --no-service to start it at login"
		}
		detail := d.Detail
		if !d.Found && d.Meaning != "" && !d.Skipped {
			detail += " — " + d.Meaning
		}
		add(d.ID, d.Name, lvl, detail, fix)
	}

	// The binary.
	if path, err := e.LookPath("gateway"); err != nil {
		lvl := Fail
		if _, serr := os.Stat(p.Binary); serr == nil {
			lvl = Warn
		}
		add("binary", "gateway on PATH", lvl, "not on PATH (version "+e.Version+")",
			"add "+p.tilde(p.BinDir)+" to PATH in your shell's startup file, or run install.sh")
	} else if rp, _ := filepath.EvalSymlinks(path); rp != p.Binary && path != p.Binary && !sameFile(path, p.Binary) {
		add("binary", "gateway on PATH", Warn, p.tilde(path)+" comes before "+p.tilde(p.Binary)+" (version "+e.Version+")",
			"remove the other copy, or put "+p.tilde(p.BinDir)+" first on PATH")
	} else {
		add("binary", "gateway on PATH", Pass, p.tilde(path)+" (version "+e.Version+")", "")
	}

	// The config.
	cfg := config.Defaults()
	cfgOK := false
	if _, ok, _ := readMaybe(p.Config); !ok {
		add("config", "Config", Fail, p.tilde(p.Config)+" not found", "run gateway install (it writes a default config)")
	} else if loaded, err := e.LoadConfig(p.Config); err != nil {
		add("config", "Config", Fail, p.tilde(p.Config)+": "+err.Error(), "fix the key named, see config.example.yaml")
	} else {
		cfg, cfgOK = loaded, true
		// LoadConfig already refuses a listener off loopback; this is the same rule,
		// for a loader that does not (spec 022).
		if e := config.CheckListeners(cfg); e != nil {
			add("config", "Config", Fail, p.tilde(p.Config)+": "+e.Message(), "fix the key named, see config.example.yaml")
		} else {
			add("config", "Config", Pass, p.tilde(p.Config)+" valid, loopback only", "")
		}
	}

	// The service.
	svc := e.service()
	if noService {
		add("service", "Login service", Warn, "not installed (--no-service): the gateway won't start at login",
			"run gateway install without --no-service, or start it by hand: "+manualStart(p))
	} else if svc.Kind() != "none" {
		s := svc.Status(p)
		loopErr, looping := startupLoop(e, p, cfg, cfgOK)
		switch {
		case s.Installed && s.Loaded && looping:
			// Running or not at this instant, it is failing its start every few seconds
			// (spec 025 AC5): the error says why, the state alone would not.
			add("service", "Login service", Fail, "service is restarting after a startup error: "+loopErr,
				"fix the error named (see the log: "+svc.LogHint(p)+"); the service tries again every "+
					strconv.Itoa(int(startup.Wait.Seconds()))+" s on its own")
		case !s.Installed:
			add("service", "Login service", Fail, p.tilde(svc.File(p))+" not found", "run gateway install")
		case !s.Loaded:
			add("service", "Login service", Fail, "installed, not loaded", "run: "+svc.LoadHint(p))
		case !s.Running:
			add("service", "Login service", Fail, "loaded, not running", "see the log: "+svc.LogHint(p)+"; then run: "+svc.StartHint(p))
		case !s.AtLogin:
			add("service", "Login service", Warn, "running, but not started at login", "run gateway install (it enables the service)")
		default:
			add("service", "Login service", Pass, svc.Kind()+": installed, running, starts at login", "")
		}
	}

	// The listeners.
	out = append(out, listenerChecks(e, cfg, cfgOK)...)

	// The settings file.
	if data, ok, _ := readMaybe(p.Settings); !ok {
		add("settings", c.Name+" settings", Fail, p.tilde(p.Settings)+" not found", "run gateway install")
	} else if why := c.CheckSettings(data, cfg); why != "" {
		add("settings", c.Name+" settings", Fail, why, "run gateway install (it rewrites the settings file from your config)")
	} else {
		add("settings", c.Name+" settings", Pass, p.tilde(p.Settings)+" points at "+cfg.ListenAddr, "")
	}

	// The shell function.
	out = append(out, functionChecks(e, c, p, st)...)

	// Hooks.
	hooks := "http://" + cfg.ListenAddr + c.HooksPath
	if code, err := e.HTTPStatus(hooks); err != nil {
		add("hooks", "Hooks", Fail, hooks+" unreachable", "start the gateway (see Login service)")
	} else if code == 404 {
		add("hooks", "Hooks", Fail, hooks+" answered 404", "upgrade the gateway: run install.sh again")
	} else {
		add("hooks", "Hooks", Pass, hooks+" reachable", "")
	}

	// The store.
	out = append(out, storeCheck(e, p, cfg))

	// Leftovers of an older install.
	out = append(out, leftoverCheck(e, c, p, st))

	if stErr != nil {
		add("state", "Install state", Warn, stErr.Error(), "run gateway install to rewrite it")
	}
	return out
}

// uiHealthPath is the UI listener's health route.
const uiHealthPath = "/api/health"

// uiWarmupGrace is how long after a start a silent UI counts as warming up, not broken:
// the warm-up was 22.5 s on the real store (roadmap), so 120 s leaves room to grow.
const uiWarmupGrace = 120 * time.Second

var startTimeLine = regexp.MustCompile(`(?m)^process_start_time_seconds ([0-9.e+]+)$`)

// uiWarming judges a UI that does not answer while the API does: the UI accepts only
// after its start-up warm-up, so within uiWarmupGrace of the gateway's start it is a
// warn, after it a fail. The start comes from process_start_time_seconds on the API's
// /metrics (023 research D26).
func uiWarming(e *Env, cfg config.Config, r Result, addr string) Result {
	var started time.Time
	if e.Fetch != nil {
		if body, err := e.Fetch("http://" + cfg.ListenAddr + "/metrics"); err == nil {
			if m := startTimeLine.FindStringSubmatch(body); m != nil {
				if f, perr := strconv.ParseFloat(m[1], 64); perr == nil {
					started = time.Unix(0, int64(f*float64(time.Second)))
				}
			}
		}
	}
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	switch {
	case started.IsZero():
		r.Level = Warn
		r.Detail = addr + " not answering yet while the API does: most likely warming up"
		r.Fix = "wait about 30 s and run doctor again; if it still fails, see the gateway's log"
	case now().Sub(started) < uiWarmupGrace:
		r.Level = Warn
		r.Detail = fmt.Sprintf("%s warming up, about %d s since the gateway started", addr, int(now().Sub(started).Round(time.Second).Seconds()))
		r.Fix = "the UI opens when its warm-up ends (about 20–30 s on a large store): wait and run doctor again"
	default:
		r.Level = Fail
		r.Detail = fmt.Sprintf("%s does not answer, %d s after the gateway started", addr, int(now().Sub(started).Round(time.Second).Seconds()))
		r.Fix = "past the warm-up: see the gateway's log (Login service) and check ui.listen_addr in the config"
	}
	return r
}

func listenerChecks(e *Env, cfg config.Config, cfgOK bool) []Result {
	type l struct {
		id, name, addr string
		health         string // the health path, "" for a TCP connect
	}
	ls := []l{{"listener-api", "API listener", cfg.ListenAddr, "/healthz"}}
	if cfg.UI.Enabled {
		// The UI's own health route; it has no /healthz (spec 009).
		ls = append(ls, l{"listener-ui", "UI listener", cfg.UI.ListenAddr, uiHealthPath})
	}
	if cfg.Effects.Enabled && cfg.Effects.Network.Enabled {
		ls = append(ls, l{"listener-http-proxy", "HTTP proxy", cfg.Effects.Network.HTTPProxyAddr, ""})
		if cfg.Effects.Network.SocksProxyAddr != "" {
			ls = append(ls, l{"listener-socks", "SOCKS proxy", cfg.Effects.Network.SocksProxyAddr, ""})
		}
	}
	var out []Result
	apiUp := false
	for _, x := range ls {
		r := Result{ID: x.id, Name: x.name, Link: anchor("listeners")}
		ok := false
		if x.health != "" {
			code, err := e.HTTPStatus("http://" + x.addr + x.health)
			ok = err == nil && code == 200
		} else {
			ok = e.Dial(x.addr)
		}
		if x.id == "listener-api" {
			apiUp = ok
		}
		if ok {
			r.Level, r.Detail = Pass, x.addr+" answers"
		} else if x.id == "listener-ui" && apiUp {
			r = uiWarming(e, cfg, r, x.addr)
		} else {
			r.Level, r.Detail = Fail, x.addr+" does not answer"
			r.Fix = "start the gateway (see Login service); if it runs, check the port in the config"
			if x.id == "listener-ui" {
				r.Fix = "the UI accepts only after its start-up warm-up (up to about 30 s on a large store): wait and run doctor again; else start the gateway"
			}
			if !cfgOK {
				r.Fix = "fix the config first"
			}
		}
		out = append(out, r)
	}
	return out
}

func functionChecks(e *Env, c Client, p Paths, st *State) []Result {
	r := Result{ID: "function", Name: c.Binary + " function", Link: anchor("function")}
	_, rc := shellRC(e)
	if rc == "" {
		r.Level, r.Detail = Warn, "shell is not zsh or bash: cannot check"
		r.Fix = "add the function by hand (docs/install.md)"
		return []Result{r}
	}
	f := readRC(rc, c.Binary)
	n := len(f.defs)
	if f.block != nil {
		n++
	}
	switch {
	case n > 1:
		r.Level, r.Detail = Fail, fmt.Sprintf("defined %d times in %s", n, p.tilde(rc))
		r.Fix = "run gateway install and let it replace the hand-written one, or delete one by hand"
	case f.block != nil:
		r.Level, r.Detail = Pass, "managed block in "+p.tilde(rc)
	case n == 1:
		r.Level, r.Detail = Warn, "hand-written in "+p.tilde(rc)+" (not managed by install)"
		r.Fix = "run gateway install and let it replace it"
	default:
		r.Level, r.Detail = Fail, "not found in "+p.tilde(rc)
		r.Fix = "run gateway install"
		if st != nil && st.Shell == nil {
			r.Level, r.Fix = Warn, "installed with --no-shell: add it by hand, or run gateway install"
		}
	}
	return []Result{r}
}

func storeCheck(e *Env, p Paths, cfg config.Config) Result {
	r := Result{ID: "store", Name: "Store", Link: anchor("store")}
	dir := cfg.Capture.Dir
	if dir == "" {
		dir = p.DataDir
	}
	if _, err := os.Stat(dir); err != nil {
		r.Level, r.Detail = Warn, p.tilde(dir)+" does not exist yet"
		r.Fix = "start the gateway: it creates the store on its first start"
		return r
	}
	f, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		r.Level, r.Detail, r.Fix = Fail, p.tilde(dir)+" not writable", "make the folder writable by you"
		return r
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	size := dirSize(dir)
	free, ok := e.FreeBytes(dir)
	r.Level = Pass
	r.Detail = fmt.Sprintf("%s writable, %s used", p.tilde(dir), humanBytes(size))
	if ok {
		r.Detail += ", " + humanBytes(free) + " free"
		if free < 1<<30 {
			r.Level, r.Fix = Warn, "free some disk space: under 1 GiB left"
		}
	}
	return r
}

func leftoverCheck(e *Env, c Client, p Paths, st *State) Result {
	r := Result{ID: "leftover", Name: "Older install", Link: anchor("leftover")}
	var found []string
	if file := e.service().File(p); file != "" {
		if data, ok, _ := readMaybe(file); ok && !st.owns(file) && !strings.Contains(string(data), managedMark) {
			found = append(found, p.tilde(file)+" (not made by install)")
		}
	}
	_, rc := shellRC(e)
	for _, f := range append([]string{rc}, otherRCs(e, rc)...) {
		if f == "" {
			continue
		}
		if rf := readRC(f, c.Binary); len(rf.defs) > 0 {
			found = append(found, fmt.Sprintf("a hand-written %s function in %s (line %d)", c.Binary, p.tilde(f), rf.defs[0].Start+1))
		}
	}
	if len(found) == 0 {
		r.Level, r.Detail = Pass, "none found"
		return r
	}
	r.Level, r.Detail = Warn, strings.Join(found, "; ")
	r.Fix = "run gateway install to replace it in your shell's file; remove one in another shell's file by hand"
	return r
}

// startupLoop reports the startup error the service's gateway last recorded, if its
// start failed within the last few waits and the API does not answer now (spec 025 AC5).
func startupLoop(e *Env, p Paths, cfg config.Config, cfgOK bool) (string, bool) {
	if p.StartupError == "" {
		return "", false
	}
	r, err := startup.Read(p.StartupError)
	if err != nil {
		return "", false
	}
	now := time.Now
	if e.Now != nil {
		now = e.Now
	}
	if !startup.Restarting(r, now()) {
		return "", false
	}
	if cfgOK && e.HTTPStatus != nil {
		if code, err := e.HTTPStatus("http://" + cfg.ListenAddr + "/healthz"); err == nil && code == 200 {
			return "", false // it started since: the record is from before
		}
	}
	return r.Error, true
}
