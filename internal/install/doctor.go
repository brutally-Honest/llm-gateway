package install

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/brutally-honest/llm-gateway/internal/config"
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
		if bad := nonLoopback(cfg); bad != "" {
			add("config", "Config", Fail, bad+" is not loopback", "set it to a 127.0.0.1 address: the gateway has no auth")
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
		switch {
		case !s.Installed:
			add("service", "Login service", Fail, p.tilde(svc.File(p))+" not found", "run gateway install")
		case !s.Running:
			add("service", "Login service", Fail, "installed, not running", "see the log: "+svc.LogHint(p)+"; then gateway install restarts it")
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
	for _, x := range ls {
		r := Result{ID: x.id, Name: x.name, Link: anchor("listeners")}
		ok := false
		if x.health != "" {
			code, err := e.HTTPStatus("http://" + x.addr + x.health)
			ok = err == nil && code == 200
		} else {
			ok = e.Dial(x.addr)
		}
		if ok {
			r.Level, r.Detail = Pass, x.addr+" answers"
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

// loopback says whether a host:port listens on loopback only.
func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func nonLoopback(cfg config.Config) string {
	if !loopback(cfg.ListenAddr) {
		return "listen_addr " + cfg.ListenAddr
	}
	n := cfg.Effects.Network
	if cfg.Effects.Enabled && n.Enabled {
		if !loopback(n.HTTPProxyAddr) {
			return "effects.network.http_proxy_addr " + n.HTTPProxyAddr
		}
		if n.SocksProxyAddr != "" && !loopback(n.SocksProxyAddr) {
			return "effects.network.socks_proxy_addr " + n.SocksProxyAddr
		}
	}
	return ""
}
