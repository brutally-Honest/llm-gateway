package config

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// fileConfig is the YAML file's shape. Its yaml tags are the set of known keys.
// A nil field is a key the file does not set.
type fileConfig struct {
	ListenAddr      *string `yaml:"listen_addr"`
	LogLevel        *string `yaml:"log_level"`
	ShutdownTimeout *string `yaml:"shutdown_timeout"`

	Upstreams map[string]upstreamFile `yaml:"upstreams"`
}

// upstreamFile is one upstreams.<name> block. Like fileConfig, its tags are the known
// keys.
type upstreamFile struct {
	BaseURL               *string `yaml:"base_url"`
	ConnectTimeout        *string `yaml:"connect_timeout"`
	TLSHandshakeTimeout   *string `yaml:"tls_handshake_timeout"`
	ResponseHeaderTimeout *string `yaml:"response_header_timeout"`
}

// yamlKey is the yaml key of a struct field.
func yamlKey(f reflect.StructField) string {
	key, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
	return key
}

// stringFields returns s's *string fields by yaml key.
func stringFields(s any) map[string]*string {
	v := reflect.ValueOf(s).Elem()
	t := v.Type()
	values := map[string]*string{}
	for i := range t.NumField() {
		if p, ok := v.Field(i).Interface().(*string); ok {
			values[yamlKey(t.Field(i))] = p
		}
	}
	return values
}

// fileValues returns f's fields by dotted key: "log_level", "upstreams.x.base_url".
func fileValues(f *fileConfig) map[string]*string {
	values := stringFields(f)
	for name, u := range f.Upstreams {
		for field, p := range stringFields(&u) {
			values[upstreamKey(name, field)] = p
		}
	}
	return values
}

// Load returns the configuration: defaults, then the file, then env (Config steps 1–6
// in specs/000-foundation/plan.md). Every error is an *Error.
func Load(opts Options) (Config, Source, error) {
	cfg := Defaults()
	src := Source{EnvOverrides: []string{}}
	settings := settingsFor(opts.Upstreams)
	if len(opts.Upstreams) > 0 {
		cfg.Upstreams = make(map[string]Upstream, len(opts.Upstreams))
	}
	for _, spec := range opts.Upstreams {
		up, ok := defaultUpstream(spec)
		if !ok {
			return Config{}, Source{}, &Error{Key: upstreamKey(spec.Name, fieldBaseURL), Source: "default", Reason: reasonInvalidURL}
		}
		cfg.Upstreams[spec.Name] = up
	}

	path, data, err := readFile(opts)
	if err != nil {
		return Config{}, Source{}, err
	}
	if path != "" {
		values, lines, err := parseFile(path, data, opts.Upstreams)
		if err != nil {
			return Config{}, Source{}, err
		}
		for _, s := range settings {
			v := values[s.key]
			if v == nil {
				continue
			}
			if reason := s.apply(&cfg, *v); reason != "" {
				return Config{}, Source{}, &Error{Key: s.key, Source: path, Reason: reason, Line: lines[s.key]}
			}
		}
		src.File = path
	}

	if opts.LookupEnv != nil {
		for _, s := range settings {
			name := envName(s.key)
			v, ok := opts.LookupEnv(name)
			// Set but empty counts as unset (ADR 0002).
			if !ok || v == "" {
				continue
			}
			if reason := s.apply(&cfg, v); reason != "" {
				return Config{}, Source{}, &Error{Key: s.key, Source: name, Reason: reason}
			}
			src.EnvOverrides = append(src.EnvOverrides, s.key)
		}
	}
	return cfg, src, nil
}

// readFile picks the file (step 1). It returns "" and no error when there is no file.
func readFile(opts Options) (string, []byte, error) {
	path, explicit := opts.Path, opts.Path != ""
	if !explicit {
		path = opts.DefaultPath
	}
	if path == "" {
		return "", nil, nil
	}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		return path, data, nil
	case errors.Is(err, fs.ErrNotExist) && !explicit:
		return "", nil, nil
	case errors.Is(err, fs.ErrNotExist):
		return "", nil, &Error{Source: path, Reason: reasonFileNotFound}
	default:
		return "", nil, &Error{Source: path, Reason: reasonCannotRead}
	}
}

// syntaxLine matches yaml.v3's syntax errors, "yaml: line N: <problem>". Only N is
// kept: the problem text can quote the value.
var syntaxLine = regexp.MustCompile(`^yaml: line (\d+):`)

// parseFile checks the file's structure and returns its values and the line of each
// key it sets (steps 2–4).
func parseFile(path string, data []byte, specs []UpstreamSpec) (map[string]*string, map[string]int, error) {
	fail := func(key, reason string, line int) error {
		return &Error{Key: key, Source: path, Reason: reason, Line: line}
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		line := 0
		if m := syntaxLine.FindStringSubmatch(err.Error()); m != nil {
			line, _ = strconv.Atoi(m[1])
		}
		return nil, nil, fail("", reasonMalformedYAML, line)
	}

	known := stringFields(&fileConfig{})
	knownFields := stringFields(&upstreamFile{})
	names := map[string]bool{}
	for _, spec := range specs {
		names[spec.Name] = true
	}
	lines := map[string]int{}
	seen := map[string]bool{}
	// entry checks one key node: a scalar key that is known and not repeated.
	entry := func(dotted string, k *yaml.Node, isKnown bool) error {
		if !isKnown || k.Kind != yaml.ScalarNode {
			return fail(dotted, reasonUnknownKey, k.Line)
		}
		// yaml.v3 only checks duplicates when decoding into a map or struct.
		if seen[dotted] {
			return fail(dotted, reasonDuplicateKey, k.Line)
		}
		seen[dotted] = true
		return nil
	}
	// A null value (`key:` or `key: ~`) sets nothing, at any depth.
	isNull := func(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.Tag == "!!null" }
	// walkUpstreams checks the block under `upstreams`: names, then their fields.
	walkUpstreams := func(k, v *yaml.Node) error {
		if isNull(v) {
			return nil
		}
		if v.Kind != yaml.MappingNode {
			return fail(k.Value, reasonInvalidType, k.Line)
		}
		for i := 0; i+1 < len(v.Content); i += 2 {
			nk, nv := v.Content[i], v.Content[i+1]
			nameKey := "upstreams." + nk.Value
			if err := entry(nameKey, nk, names[nk.Value]); err != nil {
				return err
			}
			if isNull(nv) {
				continue
			}
			if nv.Kind != yaml.MappingNode {
				return fail(nameKey, reasonInvalidType, nk.Line)
			}
			for j := 0; j+1 < len(nv.Content); j += 2 {
				fk, fv := nv.Content[j], nv.Content[j+1]
				dotted := upstreamKey(nk.Value, fk.Value)
				_, isKnown := knownFields[fk.Value]
				if err := entry(dotted, fk, isKnown); err != nil {
					return err
				}
				if fv.Kind != yaml.ScalarNode {
					return fail(dotted, reasonInvalidType, fk.Line)
				}
				lines[dotted] = fk.Line
			}
		}
		return nil
	}
	if root := documentRoot(&doc); root != nil {
		if root.Kind != yaml.MappingNode {
			return nil, nil, fail("", reasonNotMapping, root.Line)
		}
		for i := 0; i+1 < len(root.Content); i += 2 {
			k, v := root.Content[i], root.Content[i+1]
			_, isKnown := known[k.Value]
			if err := entry(k.Value, k, isKnown || k.Value == "upstreams"); err != nil {
				return nil, nil, err
			}
			if k.Value == "upstreams" {
				if err := walkUpstreams(k, v); err != nil {
					return nil, nil, err
				}
				continue
			}
			if v.Kind != yaml.ScalarNode {
				return nil, nil, fail(k.Value, reasonInvalidType, k.Line)
			}
			lines[k.Value] = k.Line
		}
	}

	// (*Node).Decode has no KnownFields, so decode the raw bytes. After the walk
	// this fails only on a value the struct can't hold, like `!!int abc`. Its
	// message can quote the value, so only the reason is kept.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var f fileConfig
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) { // an empty or comment-only file
			return map[string]*string{}, lines, nil
		}
		return nil, nil, fail("", reasonInvalidConfig, 0)
	}
	var next yaml.Node
	if err := dec.Decode(&next); !errors.Is(err, io.EOF) {
		return nil, nil, fail("", reasonMultipleDocuments, next.Line)
	}
	return fileValues(&f), lines, nil
}

// documentRoot is the document's top node, or nil for an empty document: an empty
// or comment-only file, or a bare `---` or `~`.
func documentRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		return nil
	}
	return root
}
