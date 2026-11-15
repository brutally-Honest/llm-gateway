package observability

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// lastShutdownFile is where a stop leaves its record, in the capture directory: the
// process keeps no state, and a stop's numbers are only scrapeable after the next start.
const lastShutdownFile = "last-shutdown.json"

// LastShutdown is what one stop took and cut (spec 021 AC7).
type LastShutdown struct {
	At         time.Time `json:"at"`
	Duration   float64   `json:"duration_seconds"`
	CallsCut   int64     `json:"calls_cut"`
	TunnelsCut int64     `json:"tunnels_cut"`
}

// SaveLastShutdown writes s to dir, replacing the record of the stop before.
func SaveLastShutdown(dir string, s LastShutdown) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, lastShutdownFile+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, lastShutdownFile))
}

// LoadLastShutdown reads the last stop's record from dir; false with no error when
// there is none yet.
func LoadLastShutdown(dir string) (LastShutdown, bool, error) {
	b, err := os.ReadFile(filepath.Join(dir, lastShutdownFile))
	if errors.Is(err, fs.ErrNotExist) {
		return LastShutdown{}, false, nil
	}
	if err != nil {
		return LastShutdown{}, false, err
	}
	var s LastShutdown
	if err := json.Unmarshal(b, &s); err != nil {
		return LastShutdown{}, false, err
	}
	return s, true, nil
}

// LastShutdown exposes the previous stop's record as gauges.
func (m *Metrics) LastShutdown(s LastShutdown) {
	if m == nil {
		return
	}
	for _, g := range []struct {
		name, help string
		v          float64
	}{
		{"gateway_last_shutdown_duration_seconds", "How long the previous stop took, from the stop signal to exit.", s.Duration},
		{"gateway_last_shutdown_calls_cut", "Model calls the previous stop cut when its grace period ran out.", float64(s.CallsCut)},
		{"gateway_last_shutdown_tunnels_cut", "Proxy tunnels the previous stop cut.", float64(s.TunnelsCut)},
		{"gateway_last_shutdown_timestamp_seconds", "When the previous stop ended, in Unix seconds.", float64(s.At.UnixNano()) / 1e9},
	} {
		v := g.v
		m.GaugeFunc(g.name, g.help, func() float64 { return v })
	}
}
