// Package startup is the crash-loop backoff of a gateway run as a login service: how the
// gateway tells it runs under launchd or systemd, how long a failed start waits before
// exiting, and the record of the last failed start that the gateway writes and doctor
// reads (spec 025 AC5, research D7).
package startup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	// Wait is how long a failed start under a service waits before it exits, so the
	// service manager restarts it about every Wait instead of every ThrottleInterval.
	Wait = 10 * time.Second
	// LogEvery is how often the same startup error is written to the log at most.
	LogEvery = 10 * time.Minute
	// launchdLabel is the plist's Label; launchd sets XPC_SERVICE_NAME to it.
	launchdLabel = "dev.llm-gateway"
	fileName     = "startup-error.json"
	dirName      = "llm-gateway"
)

// UnderService reports whether this process is the gateway's own login service: launchd
// sets XPC_SERVICE_NAME to the job's label, and systemd sets INVOCATION_ID and
// SYSTEMD_EXEC_PID to the pid it started. A terminal opened from a service manager can
// inherit those variables, so the label and the pid must match, not just be set.
func UnderService(lookupEnv func(string) (string, bool), pid int) bool {
	if lookupEnv == nil {
		return false
	}
	if v, _ := lookupEnv("XPC_SERVICE_NAME"); v == launchdLabel {
		return true
	}
	if _, ok := lookupEnv("INVOCATION_ID"); !ok {
		return false
	}
	v, _ := lookupEnv("SYSTEMD_EXEC_PID")
	return v != "" && v == strconv.Itoa(pid)
}

// Path is the record's file: $XDG_STATE_HOME/llm-gateway/startup-error.json, else
// ~/.local/state/llm-gateway/startup-error.json, on both OSes. False when neither is an
// absolute path.
func Path(lookupEnv func(string) (string, bool)) (string, bool) {
	if lookupEnv == nil {
		return "", false
	}
	if v, ok := lookupEnv("XDG_STATE_HOME"); ok && filepath.IsAbs(v) {
		return filepath.Join(v, dirName, fileName), true
	}
	if v, ok := lookupEnv("HOME"); ok && filepath.IsAbs(v) {
		return filepath.Join(v, ".local", "state", dirName, fileName), true
	}
	return "", false
}

// Record is the last failed start under a service. Error is the startup error as the
// log gives it (the error lines' fields, never a secret: they are built from fixed
// reasons, ADR 0002); Key identifies it for the once-per-LogEvery rule.
type Record struct {
	Error    string    `json:"error"`
	Key      string    `json:"key"`
	LoggedAt time.Time `json:"logged_at"` // when this error was last written to the log
	FailedAt time.Time `json:"failed_at"` // when a start last failed with it
}

// Read reads the record at path; a missing file is os.ErrNotExist.
func Read(path string) (Record, error) {
	var r Record
	data, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, errors.New(path + ": not a startup record")
	}
	return r, nil
}

// Write writes the record at path, creating its folder.
func Write(path string, r Record) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Clear removes the record after a start that succeeded; a missing one is fine.
func Clear(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Restarting reports whether r says the service is in a startup-error loop at now: a
// start failed within the last two waits plus a margin for the manager's restart.
func Restarting(r Record, now time.Time) bool {
	return !r.FailedAt.IsZero() && now.Sub(r.FailedAt) <= 2*Wait+5*time.Second
}
