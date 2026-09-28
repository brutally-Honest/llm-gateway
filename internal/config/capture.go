package config

import (
	"path/filepath"
	"strconv"
)

// Capture is the capture pipeline's settings (spec 002, Config).
type Capture struct {
	Enabled      bool
	Dir          string // "" = DefaultCaptureDir, resolved by the caller
	QueueSize    int
	Workers      int
	MaxBodyBytes int64
	MemoryLimit  int64
}

// The fields under capture, in the order they are applied.
const (
	fieldEnabled      = "enabled"
	fieldDir          = "dir"
	fieldQueueSize    = "queue_size"
	fieldWorkers      = "workers"
	fieldMaxBodyBytes = "max_body_bytes"
	fieldMemoryLimit  = "memory_limit"
)

// defaultCapture is capture with no file and no env. Dir is left empty: its default
// depends on the environment, which Defaults does not read.
func defaultCapture() Capture {
	return Capture{
		Enabled:      true,
		QueueSize:    256,
		Workers:      2,
		MaxBodyBytes: 32 << 20,
		MemoryLimit:  256 << 20,
	}
}

// captureDirName is the directory the default location ends in.
const captureDirName = "llm-gateway"

// DefaultCaptureDir is where the store lives when capture.dir is not set:
// $XDG_DATA_HOME/llm-gateway when XDG_DATA_HOME is absolute (a relative one is ignored,
// as the XDG Base Directory spec requires), else $HOME/.local/share/llm-gateway. HOME
// is read through lookupEnv, so callers and tests control it. It returns false when
// neither gives an absolute path.
func DefaultCaptureDir(lookupEnv func(string) (string, bool)) (string, bool) {
	if lookupEnv == nil {
		return "", false
	}
	if xdg, ok := lookupEnv("XDG_DATA_HOME"); ok && filepath.IsAbs(xdg) {
		return filepath.Join(xdg, captureDirName), true
	}
	if home, ok := lookupEnv("HOME"); ok && filepath.IsAbs(home) {
		return filepath.Join(home, ".local", "share", captureDirName), true
	}
	return "", false
}

// captureKey is the dotted key of one capture field.
func captureKey(field string) string {
	return "capture." + field
}

// parsePositive is a base-10 integer > 0 that fits in bitSize bits.
func parsePositive(v string, bitSize int) (int64, bool) {
	n, err := strconv.ParseInt(v, 10, bitSize)
	return n, err == nil && n > 0
}

// captureSettings is one setting per capture field.
func captureSettings() []setting {
	count := func(field string, set func(*Capture, int)) setting {
		return setting{captureKey(field), func(c *Config, v string) string {
			n, ok := parsePositive(v, strconv.IntSize)
			if !ok {
				return reasonInvalidValue
			}
			set(&c.Capture, int(n))
			return ""
		}}
	}
	size := func(field string, set func(*Capture, int64)) setting {
		return setting{captureKey(field), func(c *Config, v string) string {
			n, ok := parsePositive(v, 64)
			if !ok {
				return reasonInvalidValue
			}
			set(&c.Capture, n)
			return ""
		}}
	}
	return []setting{
		{captureKey(fieldEnabled), func(c *Config, v string) string {
			switch v {
			case "true":
				c.Capture.Enabled = true
			case "false":
				c.Capture.Enabled = false
			default:
				return reasonInvalidValue
			}
			return ""
		}},
		// A relative dir would put a second store wherever the binary happens to run.
		{captureKey(fieldDir), func(c *Config, v string) string {
			if !filepath.IsAbs(v) {
				return reasonInvalidValue
			}
			c.Capture.Dir = v
			return ""
		}},
		count(fieldQueueSize, func(c *Capture, n int) { c.QueueSize = n }),
		count(fieldWorkers, func(c *Capture, n int) { c.Workers = n }),
		size(fieldMaxBodyBytes, func(c *Capture, n int64) { c.MaxBodyBytes = n }),
		size(fieldMemoryLimit, func(c *Capture, n int64) { c.MemoryLimit = n }),
	}
}
