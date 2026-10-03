package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/contentcoding"
	"github.com/brutally-honest/llm-gateway/internal/logging"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// runDump is `gateway dump`: it prints one stored exchange, its bodies and its
// canonical events, and never writes gateway.db or a blob (spec 002, gateway dump).
// It knows no wire format: it prints stored bytes and canonical events only.
func runDump(d deps) int {
	boot := logging.Bootstrap(d.stdout)

	flags := flag.NewFlagSet("dump", flag.ContinueOnError)
	flags.SetOutput(io.Discard) // the flag package never prints usage text
	configPath := flags.String("config", "", "path to the config file (default ./config.yaml if present)")
	raw := flags.Bool("raw", false, "print bodies as stored, without decoding Content-Encoding")
	last := flags.Bool("last", false, "print the most recent exchange")
	switch err := flags.Parse(d.args[1:]); {
	case errors.Is(err, flag.ErrHelp):
		boot.Info("usage", zap.Strings("forms", []string{
			"dump [-config <path>] [-raw] <request-id>",
			"dump [-config <path>] [-raw] -last",
		}))
		return exitOK
	case err != nil:
		boot.Error("invalid flags")
		return exitConfig
	case flags.NArg() > 1:
		// Positional arguments can be anything, so only the count is logged.
		boot.Error("invalid flags", zap.String("reason", "unexpected argument"), zap.Int("count", flags.NArg()))
		return exitConfig
	case *last && flags.NArg() == 1:
		boot.Error("invalid flags", zap.String("reason", "request id and -last both given"))
		return exitConfig
	case !*last && flags.NArg() == 0:
		boot.Error("invalid flags", zap.String("reason", "request id or -last required"))
		return exitConfig
	}

	adapters, _ := registered()
	cfg, _, ok := loadConfig(boot, *configPath, d.lookupEnv, adapters)
	if !ok {
		return exitConfig
	}
	log := logging.New(d.stdout, cfg.LogLevel)
	dir, ok := captureDir(cfg.Capture, d.lookupEnv, log)
	if !ok {
		return exitConfig
	}

	r, err := store.OpenReader(dir)
	if err != nil {
		reason := "cannot read store"
		if errors.Is(err, fs.ErrNotExist) {
			reason = "store not found"
		}
		log.Error("dump failed", zap.String("path", dir), zap.String("reason", reason), zap.Error(err))
		return exitRuntime
	}
	defer func() { _ = r.Close() }()

	var ex store.ExchangeRow
	if *last {
		ex, err = r.Last()
	} else {
		ex, err = r.Exchange(flags.Arg(0))
	}
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// The user typed the ID, so the line only says what failed.
			log.Error("dump failed", zap.String("path", dir), zap.String("reason", "exchange not found"))
		} else {
			log.Error("dump failed", zap.String("path", dir), zap.String("reason", "cannot read store"), zap.Error(err))
		}
		return exitRuntime
	}
	if err := printExchange(d.stdout, r, ex, *raw, cfg.Capture.MaxBodyBytes); err != nil {
		log.Error("dump failed", zap.String("path", dir), zap.String("reason", "cannot read store"), zap.Error(err))
		return exitRuntime
	}
	return exitOK
}

// printExchange writes the exchange row as one JSON line, then both bodies, then its
// events as JSON lines in seq order. The output is built in memory first, so a store
// error part-way prints nothing.
func printExchange(w io.Writer, r *store.Reader, ex store.ExchangeRow, raw bool, limit int64) error {
	var out bytes.Buffer // its writes never fail
	row, err := json.Marshal(ex)
	if err != nil {
		return err
	}
	out.Write(row)
	out.WriteByte('\n')
	bodies := []struct {
		name, hash string
		header     http.Header
	}{
		{"request body", ex.RequestBody, ex.RequestHeaders},
		{"response body", ex.ResponseBody, ex.ResponseHeaders},
	}
	for _, b := range bodies {
		if err := printBody(&out, r, b.name, b.hash, b.header, raw, limit); err != nil {
			return err
		}
	}
	events, err := r.Events(ex.RequestID)
	if err != nil {
		return err
	}
	out.WriteString("== events\n")
	for _, e := range events {
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		out.Write(line)
		out.WriteByte('\n')
	}
	_, err = w.Write(out.Bytes())
	return err
}

// printBody writes one body under a header line that says what was decoded. A body
// whose Content-Encoding is unsupported is printed as stored, and the header says so.
func printBody(out *bytes.Buffer, r *store.Reader, name, hash string, header http.Header, raw bool, limit int64) error {
	if hash == "" {
		out.WriteString("== " + name + " (none)\n")
		return nil
	}
	body, err := r.Content(hash)
	if err != nil {
		return err
	}
	var note string
	if encoding := strings.Join(header.Values("Content-Encoding"), ", "); encoding != "" {
		if raw {
			note = fmt.Sprintf(" (content-encoding %s, not decoded: -raw)", encoding)
		} else {
			var status contentcoding.Status
			body, status = contentcoding.Decode(encoding, body, limit)
			switch status {
			case contentcoding.Complete:
				note = fmt.Sprintf(" (content-encoding %s, decoded)", encoding)
			case contentcoding.CutShort:
				note = fmt.Sprintf(" (content-encoding %s, decoded, cut short at %d bytes)", encoding, len(body))
			case contentcoding.Unsupported:
				note = fmt.Sprintf(" (content-encoding %s, unsupported: raw bytes)", encoding)
			}
		}
	}
	out.WriteString("== " + name + note + "\n")
	out.Write(body)
	if len(body) > 0 && body[len(body)-1] != '\n' {
		out.WriteByte('\n')
	}
	return nil
}
