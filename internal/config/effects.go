package config

import (
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Effects is the file and network effect sensors' settings (spec 014, Config).
type Effects struct {
	Enabled bool
	Files   EffectFiles
	Network EffectNetwork
}

// EffectFiles is effects.files: the checkpoint sensor.
type EffectFiles struct {
	Enabled bool
	// ExtraDirs are absolute folders watched for every session, beside the ones the
	// session names itself.
	ExtraDirs []string
	// Ignore are names matched against each part of a path, folders and file names.
	Ignore            []string
	MaxCopyBytes      int64
	GitTimeout        time.Duration
	SnapshotRetention time.Duration
}

// EffectNetwork is effects.network: the forward proxy listeners.
type EffectNetwork struct {
	Enabled        bool
	HTTPProxyAddr  string // host:port, loopback
	SocksProxyAddr string // host:port, loopback; "" turns SOCKS5 off
	Sandbox        bool   // hooks print adds a sandbox block pointing at the proxy ports
	ActiveWindow   time.Duration
}

// effectsFile is the effects block. Like fileConfig, its tags are the known keys.
type effectsFile struct {
	Enabled *string            `yaml:"enabled"`
	Files   effectsFilesFile   `yaml:"files"`
	Network effectsNetworkFile `yaml:"network"`
}

// effectsFilesFile is effects.files. The two lists are sequences in the file.
type effectsFilesFile struct {
	Enabled           *string   `yaml:"enabled"`
	ExtraDirs         *[]string `yaml:"extra_dirs"`
	Ignore            *[]string `yaml:"ignore"`
	MaxCopyBytes      *string   `yaml:"max_copy_bytes"`
	GitTimeout        *string   `yaml:"git_timeout"`
	SnapshotRetention *string   `yaml:"snapshot_retention"`
}

// effectsNetworkFile is effects.network.
type effectsNetworkFile struct {
	Enabled        *string `yaml:"enabled"`
	HTTPProxyAddr  *string `yaml:"http_proxy_addr"`
	SocksProxyAddr *string `yaml:"socks_proxy_addr"`
	Sandbox        *string `yaml:"sandbox"`
	ActiveWindow   *string `yaml:"active_window"`
}

// The dotted keys validateEffects names.
const (
	effectsHTTPProxyKey  = "effects.network.http_proxy_addr"
	effectsSocksProxyKey = "effects.network.socks_proxy_addr"
)

// reasonSameAsOther is the reason for a proxy address another listener already uses.
const reasonSameAsOther = "same as another listener"

// listSep joins a list's items into one setting value. An env var holds a list as
// comma-separated items; an item in the file must not hold one.
const listSep = ","

// defaultIgnore leaves .env out on purpose, so changes to .env files are seen.
func defaultIgnore() []string {
	return []string{".git", "node_modules", "dist", "build", "target", ".venv", "venv", "__pycache__", ".next", ".cache"}
}

// defaultEffects is the effects block with no file and no env.
func defaultEffects() Effects {
	return Effects{
		Enabled: true,
		Files: EffectFiles{
			Enabled:           true,
			ExtraDirs:         []string{},
			Ignore:            defaultIgnore(),
			MaxCopyBytes:      256 << 10,
			GitTimeout:        10 * time.Second,
			SnapshotRetention: 7 * 24 * time.Hour,
		},
		Network: EffectNetwork{
			Enabled:        true,
			HTTPProxyAddr:  "127.0.0.1:7198",
			SocksProxyAddr: "127.0.0.1:7195",
			Sandbox:        true,
			ActiveWindow:   10 * time.Minute,
		},
	}
}

func effectsKey(field string) string        { return "effects." + field }
func effectsFilesKey(field string) string   { return "effects.files." + field }
func effectsNetworkKey(field string) string { return "effects.network." + field }

// effectsValues adds the effects block's values to values, by dotted key. A list is
// joined with listSep.
func effectsValues(f *effectsFile, values map[string]*string) {
	values[effectsKey("enabled")] = f.Enabled
	for field, p := range stringFields(&f.Files) {
		values[effectsFilesKey(field)] = p
	}
	for field, p := range stringFields(&f.Network) {
		values[effectsNetworkKey(field)] = p
	}
	join := func(field string, l *[]string) {
		var v *string // nil: the file does not set it
		if l != nil {
			s := strings.Join(*l, listSep)
			v = &s
		}
		values[effectsFilesKey(field)] = v
	}
	join("extra_dirs", f.Files.ExtraDirs)
	join("ignore", f.Files.Ignore)
}

// effectsListFields are the effects.files fields that hold a sequence.
var effectsListFields = map[string]bool{"extra_dirs": true, "ignore": true}

// walkEffectsBlock checks the block under `effects`: enabled, then the files and network
// blocks of fields. entry, fail and lines are parseFile's.
func walkEffectsBlock(v *yaml.Node, entry func(string, *yaml.Node, bool) error,
	fail func(string, string, int) error, lines map[string]int) error {
	isNull := func(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.Tag == "!!null" }
	fields := func(n *yaml.Node, key func(string) string, known map[string]*string, lists map[string]bool) error {
		for j := 0; j+1 < len(n.Content); j += 2 {
			fk, fv := n.Content[j], n.Content[j+1]
			dotted := key(fk.Value)
			_, isKnown := known[fk.Value]
			if err := entry(dotted, fk, isKnown || lists[fk.Value]); err != nil {
				return err
			}
			lines[dotted] = fk.Line
			if !lists[fk.Value] {
				if fv.Kind != yaml.ScalarNode {
					return fail(dotted, reasonInvalidType, fk.Line)
				}
				continue
			}
			if isNull(fv) {
				continue
			}
			if fv.Kind != yaml.SequenceNode {
				return fail(dotted, reasonInvalidType, fk.Line)
			}
			for _, item := range fv.Content {
				if item.Kind != yaml.ScalarNode {
					return fail(dotted, reasonInvalidType, fk.Line)
				}
				if item.Value == "" || strings.Contains(item.Value, listSep) {
					return fail(dotted, reasonInvalidValue, fk.Line)
				}
			}
		}
		return nil
	}
	block := func(k, n *yaml.Node, dotted string, key func(string) string, known map[string]*string, lists map[string]bool) error {
		if isNull(n) {
			return nil
		}
		if n.Kind != yaml.MappingNode {
			return fail(dotted, reasonInvalidType, k.Line)
		}
		return fields(n, key, known, lists)
	}
	for i := 0; i+1 < len(v.Content); i += 2 {
		k, n := v.Content[i], v.Content[i+1]
		dotted := effectsKey(k.Value)
		switch k.Value {
		case "files":
			if err := entry(dotted, k, true); err != nil {
				return err
			}
			if err := block(k, n, dotted, effectsFilesKey, stringFields(&effectsFilesFile{}), effectsListFields); err != nil {
				return err
			}
		case "network":
			if err := entry(dotted, k, true); err != nil {
				return err
			}
			if err := block(k, n, dotted, effectsNetworkKey, stringFields(&effectsNetworkFile{}), nil); err != nil {
				return err
			}
		default:
			if err := entry(dotted, k, k.Value == "enabled"); err != nil {
				return err
			}
			if n.Kind != yaml.ScalarNode {
				return fail(dotted, reasonInvalidType, k.Line)
			}
			lines[dotted] = k.Line
		}
	}
	return nil
}

// effectsSettings is one setting per effects field.
func effectsSettings() []setting {
	boolean := func(key string, set func(*Effects, bool)) setting {
		return setting{key, func(c *Config, v string) string {
			switch v {
			case "true":
				set(&c.Effects, true)
			case "false":
				set(&c.Effects, false)
			default:
				return reasonInvalidValue
			}
			return ""
		}}
	}
	duration := func(key string, set func(*Effects, time.Duration)) setting {
		return setting{key, func(c *Config, v string) string {
			d, ok := parseDuration(v)
			if !ok {
				return reasonInvalidDuration
			}
			set(&c.Effects, d)
			return ""
		}}
	}
	address := func(key string, empty bool, set func(*Effects, string)) setting {
		return setting{key, func(c *Config, v string) string {
			if (v != "" || !empty) && !validAddress(v) {
				return reasonInvalidAddress
			}
			set(&c.Effects, v)
			return ""
		}}
	}
	return []setting{
		boolean(effectsKey("enabled"), func(e *Effects, b bool) { e.Enabled = b }),
		boolean(effectsFilesKey("enabled"), func(e *Effects, b bool) { e.Files.Enabled = b }),
		{effectsFilesKey("extra_dirs"), func(c *Config, v string) string {
			dirs := splitList(v)
			for _, d := range dirs {
				if !filepath.IsAbs(d) {
					return reasonInvalidValue
				}
			}
			c.Effects.Files.ExtraDirs = dirs
			return ""
		}},
		{effectsFilesKey("ignore"), func(c *Config, v string) string {
			names := splitList(v)
			for _, n := range names {
				if strings.ContainsRune(n, '/') {
					return reasonInvalidValue
				}
			}
			c.Effects.Files.Ignore = names
			return ""
		}},
		{effectsFilesKey("max_copy_bytes"), func(c *Config, v string) string {
			n, ok := parsePositive(v, 64)
			if !ok {
				return reasonInvalidValue
			}
			c.Effects.Files.MaxCopyBytes = n
			return ""
		}},
		duration(effectsFilesKey("git_timeout"), func(e *Effects, d time.Duration) { e.Files.GitTimeout = d }),
		duration(effectsFilesKey("snapshot_retention"), func(e *Effects, d time.Duration) { e.Files.SnapshotRetention = d }),
		boolean(effectsNetworkKey("enabled"), func(e *Effects, b bool) { e.Network.Enabled = b }),
		address(effectsHTTPProxyKey, false, func(e *Effects, v string) { e.Network.HTTPProxyAddr = v }),
		address(effectsSocksProxyKey, true, func(e *Effects, v string) { e.Network.SocksProxyAddr = v }),
		boolean(effectsNetworkKey("sandbox"), func(e *Effects, b bool) { e.Network.Sandbox = b }),
		duration(effectsNetworkKey("active_window"), func(e *Effects, d time.Duration) { e.Network.ActiveWindow = d }),
	}
}

// splitList splits a list setting's value; "" is the empty list.
func splitList(v string) []string {
	out := []string{}
	for _, s := range strings.Split(v, listSep) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// validateEffects runs after file and env are applied. With the network sensor on, each
// proxy address must be loopback and differ from every other listener. origin says
// where each key came from.
func validateEffects(c *Config, origin map[string]string) error {
	if !c.Effects.Enabled || !c.Effects.Network.Enabled {
		return nil
	}
	others := []string{c.ListenAddr}
	if c.UI.Enabled {
		others = append(others, c.UI.ListenAddr)
	}
	check := func(key, addr string) error {
		src := origin[key]
		if src == "" {
			src = "default"
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return &Error{Key: key, Source: src, Reason: reasonInvalidAddress}
		}
		if !loopbackHost(host) {
			return &Error{Key: key, Source: src, Reason: reasonNotLoopback}
		}
		for _, o := range others {
			if sameListen(addr, o) {
				return &Error{Key: key, Source: src, Reason: reasonSameAsOther}
			}
		}
		return nil
	}
	if err := check(effectsHTTPProxyKey, c.Effects.Network.HTTPProxyAddr); err != nil {
		return err
	}
	if c.Effects.Network.SocksProxyAddr == "" {
		return nil
	}
	others = append(others, c.Effects.Network.HTTPProxyAddr)
	return check(effectsSocksProxyKey, c.Effects.Network.SocksProxyAddr)
}

// ProxyPort is the port of a host:port address, or 0 when it has none.
func ProxyPort(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return 0
	}
	return n
}
