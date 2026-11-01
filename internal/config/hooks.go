package config

import "strconv"

// Hooks is the harness hook ingest's sink settings (spec 010, Config). They are separate
// from capture's, so a hook burst fills its own queue, not the LLM capture one.
type Hooks struct {
	QueueSize   int
	Workers     int
	MemoryLimit int64
}

// The fields under hooks, in the order they are applied.
const (
	fieldHooksQueueSize   = "queue_size"
	fieldHooksWorkers     = "workers"
	fieldHooksMemoryLimit = "memory_limit"
)

// hooksFile is the hooks block. Like fileConfig, its tags are the known keys.
type hooksFile struct {
	QueueSize   *string `yaml:"queue_size"`
	Workers     *string `yaml:"workers"`
	MemoryLimit *string `yaml:"memory_limit"`
}

// defaultHooks is the hook sink with no file and no env.
func defaultHooks() Hooks {
	return Hooks{QueueSize: 256, Workers: 1, MemoryLimit: 32 << 20}
}

// hooksKey is the dotted key of one hooks field.
func hooksKey(field string) string {
	return "hooks." + field
}

// hooksSettings is one setting per hooks field. Each must be an integer > 0.
func hooksSettings() []setting {
	count := func(field string, set func(*Hooks, int)) setting {
		return setting{hooksKey(field), func(c *Config, v string) string {
			n, ok := parsePositive(v, strconv.IntSize)
			if !ok {
				return reasonInvalidValue
			}
			set(&c.Hooks, int(n))
			return ""
		}}
	}
	return []setting{
		count(fieldHooksQueueSize, func(h *Hooks, n int) { h.QueueSize = n }),
		count(fieldHooksWorkers, func(h *Hooks, n int) { h.Workers = n }),
		{hooksKey(fieldHooksMemoryLimit), func(c *Config, v string) string {
			n, ok := parsePositive(v, 64)
			if !ok {
				return reasonInvalidValue
			}
			c.Hooks.MemoryLimit = n
			return ""
		}},
	}
}
