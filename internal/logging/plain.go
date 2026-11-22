package logging

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Plain returns a logger for the commands a person runs (dump, hooks print,
// backfill-sessions): one plain text line per entry, "<prefix>: <msg> (key: value,
// …)", never JSON. Structured logging is for the running gateway only, which logs with
// New (spec 024 AC10). level is as for New.
func Plain(w io.Writer, prefix, level string) *zap.Logger {
	lvl, err := zapcore.ParseLevel(level)
	if err != nil {
		lvl = zapcore.InfoLevel
	}
	return zap.New(&plainCore{w: zapcore.Lock(zapcore.AddSync(w)), prefix: prefix, lvl: lvl})
}

type plainCore struct {
	w      zapcore.WriteSyncer
	prefix string
	lvl    zapcore.Level
	fields []zapcore.Field
}

func (c *plainCore) Enabled(l zapcore.Level) bool { return l >= c.lvl }

func (c *plainCore) With(fields []zapcore.Field) zapcore.Core {
	cp := *c
	cp.fields = append(slices.Clone(c.fields), fields...)
	return &cp
}

func (c *plainCore) Check(e zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(e.Level) {
		return ce.AddCore(e, c)
	}
	return ce
}

func (c *plainCore) Write(e zapcore.Entry, fields []zapcore.Field) error {
	all := append(slices.Clone(c.fields), fields...)
	parts := make([]string, 0, len(all))
	for _, f := range all {
		enc := zapcore.NewMapObjectEncoder()
		f.AddTo(enc)
		for _, v := range enc.Fields {
			parts = append(parts, f.Key+": "+fmt.Sprint(v))
		}
	}
	line := c.prefix + ": " + e.Message
	if len(parts) > 0 {
		line += " (" + strings.Join(parts, ", ") + ")"
	}
	_, err := io.WriteString(c.w, line+"\n")
	return err
}

func (c *plainCore) Sync() error { return c.w.Sync() }
