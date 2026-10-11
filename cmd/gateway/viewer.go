package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"io/fs"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/store"
	"github.com/brutally-honest/llm-gateway/internal/timeline"
)

// defaultSessionLimit is how many sessions (or ungrouped exchanges) are listed
// without -n.
const defaultSessionLimit = 20

// viewer is what `gateway sessions` and `gateway timeline` share once their flags
// are parsed: the read-only store and a log for failures. They only read: nothing
// here opens the writer, so gateway.db and the blobs are never written.
type viewer struct {
	r    *store.Reader
	log  *zap.Logger
	dir  string
	name string
}

// openViewer loads config and opens the store read-only. On failure it logs one line
// and returns the exit code.
func openViewer(d deps, name, configPath string) (*viewer, int) {
	boot := logging.Bootstrap(d.stdout)
	adapters, _ := registered()
	cfg, _, ok := loadConfig(boot, configPath, d.lookupEnv, adapters)
	if !ok {
		return nil, exitConfig
	}
	log := logging.New(d.stdout, cfg.LogLevel)
	dir, ok := captureDir(cfg.Capture, d.lookupEnv, log)
	if !ok {
		return nil, exitConfig
	}
	r, err := store.OpenReader(dir)
	if err != nil {
		reason := "cannot read store"
		if errors.Is(err, fs.ErrNotExist) {
			reason = "store not found"
		}
		// The error carries the schema refusal text, as dump's does.
		log.Error(name+" failed", zap.String("path", dir), zap.String("reason", reason), zap.Error(err))
		return nil, exitRuntime
	}
	return &viewer{r: r, log: log, dir: dir, name: name}, exitOK
}

func (v *viewer) fail(reason string, err error) int {
	fields := []zap.Field{zap.String("path", v.dir), zap.String("reason", reason)}
	if err != nil {
		fields = append(fields, zap.Error(err))
	}
	v.log.Error(v.name+" failed", fields...)
	return exitRuntime
}

// flush writes the finished output in one go, so a store error part-way printed
// nothing.
func (v *viewer) flush(w io.Writer, out *bytes.Buffer) int {
	if _, err := w.Write(out.Bytes()); err != nil {
		return v.fail("cannot write output", err)
	}
	return exitOK
}

// loc is the zone times print in: local for text, UTC for JSON.
func loc(asJSON bool) *time.Location {
	if asJSON {
		return time.UTC
	}
	return time.Local
}

// runSessions is `gateway sessions`.
func runSessions(d deps) int {
	boot := logging.Bootstrap(d.stdout)
	flags := flag.NewFlagSet("sessions", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "path to the config file (default ./config.yaml if present)")
	n := flags.Int("n", defaultSessionLimit, "how many to list; 0 lists all")
	ungrouped := flags.Bool("ungrouped", false, "list the exchanges that belong to no session")
	asJSON := flags.Bool("json", false, "print JSON lines with times in UTC")
	switch err := flags.Parse(d.args[1:]); {
	case errors.Is(err, flag.ErrHelp):
		boot.Info("usage", zap.Strings("forms", []string{"sessions [-config <path>] [-n <count>] [-ungrouped] [-json]"}))
		return exitOK
	case err != nil:
		boot.Error("invalid flags")
		return exitConfig
	case flags.NArg() > 0:
		boot.Error("invalid flags", zap.String("reason", "unexpected argument"), zap.Int("count", flags.NArg()))
		return exitConfig
	case *n < 0:
		boot.Error("invalid flags", zap.String("reason", "-n must not be negative"))
		return exitConfig
	}

	v, code := openViewer(d, "sessions", *configPath)
	if v == nil {
		return code
	}
	defer func() { _ = v.r.Close() }()

	var out bytes.Buffer
	l := loc(*asJSON)
	if *ungrouped {
		rows, err := v.r.UngroupedExchanges(*n)
		if err != nil {
			return v.fail("cannot read store", err)
		}
		list := make([]timeline.UngroupedExchange, 0, len(rows))
		for _, e := range rows {
			list = append(list, timeline.UngroupedExchange{RequestID: e.RequestID, Client: e.Client,
				Method: e.Method, Path: e.Path, Start: e.StartedAt, Status: e.Status})
		}
		render := timeline.RenderUngroupedText
		if *asJSON {
			render = timeline.RenderUngroupedJSON
		}
		if err := render(&out, list, l); err != nil {
			return v.fail("cannot read store", err)
		}
		return v.flush(d.stdout, &out)
	}

	rows, err := timeline.ListSessions(v.r, *n)
	if err != nil {
		return v.fail("cannot read store", err)
	}
	sum, err := v.r.UngroupedSummary()
	if err != nil {
		return v.fail("cannot read store", err)
	}
	renderRows, renderUngrouped := timeline.RenderSessionsText, timeline.RenderUngroupedRowText
	if *asJSON {
		renderRows, renderUngrouped = timeline.RenderSessionsJSON, timeline.RenderUngroupedRowJSON
	}
	if err := renderRows(&out, rows, l); err != nil {
		return v.fail("cannot read store", err)
	}
	if sum.Count > 0 {
		u := timeline.UngroupedSummary{Count: sum.Count, First: sum.First, Last: sum.Last}
		if err := renderUngrouped(&out, u, l); err != nil {
			return v.fail("cannot read store", err)
		}
	}
	return v.flush(d.stdout, &out)
}

// runTimeline is `gateway timeline`.
func runTimeline(d deps) int {
	boot := logging.Bootstrap(d.stdout)
	flags := flag.NewFlagSet("timeline", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "path to the config file (default ./config.yaml if present)")
	last := flags.Bool("last", false, "print the session with the most recent exchange")
	full := flags.Bool("full", false, "do not cut long entries")
	asJSON := flags.Bool("json", false, "print JSON lines with times in UTC")
	switch err := flags.Parse(d.args[1:]); {
	case errors.Is(err, flag.ErrHelp):
		boot.Info("usage", zap.Strings("forms", []string{
			"timeline [-config <path>] [-full] [-json] <session-key>",
			"timeline [-config <path>] [-full] [-json] -last",
		}))
		return exitOK
	case err != nil:
		boot.Error("invalid flags")
		return exitConfig
	case flags.NArg() > 1:
		boot.Error("invalid flags", zap.String("reason", "unexpected argument"), zap.Int("count", flags.NArg()))
		return exitConfig
	case *last && flags.NArg() == 1:
		boot.Error("invalid flags", zap.String("reason", "session key and -last both given"))
		return exitConfig
	case !*last && flags.NArg() == 0:
		boot.Error("invalid flags", zap.String("reason", "session key or -last required"))
		return exitConfig
	}

	v, code := openViewer(d, "timeline", *configPath)
	if v == nil {
		return code
	}
	defer func() { _ = v.r.Close() }()

	var (
		sessions []store.SessionRow
		err      error
	)
	if *last {
		sessions, err = v.r.SessionRows(1)
	} else {
		sessions, err = v.r.SessionsByKey(flags.Arg(0))
	}
	if err != nil {
		return v.fail("cannot read store", err)
	}
	if len(sessions) == 0 {
		// The user typed the key, so the line only says what failed.
		return v.fail("session not found", nil)
	}

	var out bytes.Buffer
	l := loc(*asJSON)
	for _, s := range sessions {
		entries, sum, err := timeline.BuildWithUsage(v.r, s, timeline.Options{Full: *full})
		if err != nil {
			return v.fail("cannot read store", err)
		}
		if len(sessions) > 1 {
			if *asJSON {
				err = timeline.RenderSessionsJSON(&out, []timeline.SessionListing{{SessionRow: s}}, l)
			} else {
				err = timeline.RenderSessionHeaderText(&out, s.Client, s.PrincipalID, s.SessionKey)
			}
			if err != nil {
				return v.fail("cannot read store", err)
			}
		}
		render, renderUsage := timeline.RenderText, timeline.RenderUsageText
		if *asJSON {
			render, renderUsage = timeline.RenderJSON, timeline.RenderUsageJSON
		}
		if err := render(&out, entries, l); err != nil {
			return v.fail("cannot read store", err)
		}
		if err := renderUsage(&out, sum); err != nil {
			return v.fail("cannot read store", err)
		}
	}
	return v.flush(d.stdout, &out)
}
