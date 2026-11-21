// Package logging builds the gateway's zap loggers. It is the one place that knows
// zap's configuration. Every line the running gateway logs, zap's own errors included, is
// JSON (New); the commands a person runs log plain text (Plain).
// There is no global logger: callers pass the *zap.Logger in.
package logging

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"os"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New returns a JSON logger writing to w at level, one of the levels config accepts
// (debug, info, warn, error). Any other level logs at info. Error lines carry no
// stack trace; the only stack is the one the recover middleware adds as a field.
func New(w io.Writer, level string) *zap.Logger {
	return newLogger(w, os.Stderr, level)
}

// Bootstrap is the logger for errors before config loads: JSON, at info.
func Bootstrap(w io.Writer) *zap.Logger {
	return New(w, "info")
}

// StdLog adapts l for http.Server.ErrorLog: each line net/http writes becomes a
// warn line from the "http" logger.
func StdLog(l *zap.Logger) *log.Logger {
	// NewStdLogAt fails only for a level zap doesn't know; WarnLevel is one it does.
	std, _ := zap.NewStdLogAt(l.Named("http"), zap.WarnLevel)
	return std
}

// newLogger is New with zap's error output sent to errOut instead of stderr.
func newLogger(w, errOut io.Writer, level string) *zap.Logger {
	lvl, err := zapcore.ParseLevel(level)
	if err != nil {
		lvl = zapcore.InfoLevel
	}
	enc := zapcore.NewJSONEncoder(zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "", // never attach stacks
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.RFC3339NanoTimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	})
	core := zapcore.NewCore(enc, zapcore.Lock(zapcore.AddSync(w)), lvl)
	return zap.New(core,
		zap.AddCaller(),
		zap.ErrorOutput(zapcore.Lock(zapcore.AddSync(&jsonErrorOutput{w: errOut}))),
	)
}

// jsonErrorOutput receives zap's internal errors, which zap writes as plain text
// (for example "<time> write error: <err>"), and writes each as one JSON line.
type jsonErrorOutput struct {
	w io.Writer
}

func (o *jsonErrorOutput) Write(p []byte) (int, error) {
	line, err := json.Marshal(struct {
		Level  string `json:"level"`
		TS     string `json:"ts"`
		Msg    string `json:"msg"`
		Detail string `json:"detail"`
	}{
		Level:  "error",
		TS:     time.Now().Format(time.RFC3339Nano),
		Msg:    "logger error",
		Detail: string(bytes.TrimRight(p, "\n")),
	})
	if err != nil {
		return 0, err
	}
	if _, err := o.w.Write(append(line, '\n')); err != nil {
		return 0, err
	}
	return len(p), nil
}
