package config

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// sentinel stands in for a secret. It must never appear in an error.
const sentinel = "s3ntinel-v4lue"

// writeConfig writes content to a config file in a temp dir and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// env is a LookupEnv over the given name/value pairs.
func env(pairs ...string) func(string) (string, bool) {
	m := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return func(name string) (string, bool) {
		v, ok := m[name]
		return v, ok
	}
}

// loadErr runs Load and returns its *Error, failing the test if there is none.
func loadErr(t *testing.T, opts Options) *Error {
	t.Helper()
	_, _, err := Load(opts)
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("Load() error = %v, want a *config.Error", err)
	}
	return e
}

// AC5: with no config, the listen address is loopback on 7197.
func TestDefaults(t *testing.T) {
	want := Config{ListenAddr: "127.0.0.1:7197", LogLevel: "info", ShutdownTimeout: 10 * time.Minute}
	if got := Defaults(); !reflect.DeepEqual(got, want) {
		t.Errorf("Defaults() = %+v, want %+v", got, want)
	}

	cfg, src, err := Load(Options{DefaultPath: filepath.Join(t.TempDir(), "config.yaml"), LookupEnv: env()})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load() = %+v, want %+v", cfg, want)
	}
	if src.File != "" || src.EnvOverrides == nil || len(src.EnvOverrides) != 0 {
		t.Errorf("Source = %#v, want no file and an empty, non-nil EnvOverrides", src)
	}
}

// Every known file key must have a setting, or its value would be silently ignored.
func TestSettingsCoverFileKeys(t *testing.T) {
	var keys []string
	for key := range fileValues(&fileConfig{Upstreams: map[string]upstreamFile{"anthropic": {}}}) {
		keys = append(keys, key)
	}
	var covered []string
	for _, s := range settingsFor([]UpstreamSpec{{Name: "anthropic"}}) {
		covered = append(covered, s.key)
	}
	slices.Sort(keys)
	slices.Sort(covered)
	if !slices.Equal(keys, covered) {
		t.Errorf("file keys %v, settings %v", keys, covered)
	}
}

func TestLoad_FileLookup(t *testing.T) {
	t.Run("default_path_present", func(t *testing.T) {
		path := writeConfig(t, "log_level: debug\n")
		cfg, src, err := Load(Options{DefaultPath: path})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.LogLevel != "debug" || src.File != path {
			t.Errorf("LogLevel = %q, File = %q; want debug, %q", cfg.LogLevel, src.File, path)
		}
	})
	t.Run("explicit_path_wins", func(t *testing.T) {
		def := writeConfig(t, "log_level: debug\n")
		path := writeConfig(t, "log_level: warn\n")
		cfg, src, err := Load(Options{Path: path, DefaultPath: def})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.LogLevel != "warn" || src.File != path {
			t.Errorf("LogLevel = %q, File = %q; want warn, %q", cfg.LogLevel, src.File, path)
		}
	})
	t.Run("explicit_path_missing", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nope.yaml")
		e := loadErr(t, Options{Path: path})
		if e.Reason != reasonFileNotFound || e.Source != path {
			t.Errorf("error = %+v, want %q from %q", e, reasonFileNotFound, path)
		}
	})
}

// AC6 and AC12 at the loader: env beats the file, and an empty env var is unset.
func TestLoad_Precedence(t *testing.T) {
	path := writeConfig(t, "listen_addr: 127.0.0.1:1\nlog_level: warn\nshutdown_timeout: 5s\n")
	cfg, src, err := Load(Options{Path: path, LookupEnv: env(
		"GATEWAY_LISTEN_ADDR", "127.0.0.1:2",
		"GATEWAY_LOG_LEVEL", "",
		"GATEWAY_SHUTDOWN_TIMEOUT", "7s",
	)})
	if err != nil {
		t.Fatal(err)
	}
	want := Config{ListenAddr: "127.0.0.1:2", LogLevel: "warn", ShutdownTimeout: 7 * time.Second}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("Load() = %+v, want %+v", cfg, want)
	}
	if got, want := src.EnvOverrides, []string{"listen_addr", "shutdown_timeout"}; !slices.Equal(got, want) {
		t.Errorf("EnvOverrides = %v, want %v", got, want)
	}
}

func TestLoad_LogLevelIsLowerCased(t *testing.T) {
	cfg, _, err := Load(Options{LookupEnv: env("GATEWAY_LOG_LEVEL", "WARN")})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want warn", cfg.LogLevel)
	}
}

// AC8 at the loader: the error names the key, the file and the key's line.
func TestLoad_UnknownKey(t *testing.T) {
	path := writeConfig(t, "log_level: info\nlisten_adr: 127.0.0.1:7197\n")
	e := loadErr(t, Options{Path: path})
	want := Error{Key: "listen_adr", Source: path, Reason: reasonUnknownKey, Line: 2}
	if *e != want {
		t.Errorf("error = %+v, want %+v", *e, want)
	}
}

// AC11 at the loader: one case per reason in Config steps 1–5, each with the
// sentinel where a value would be.
func TestLoad_ErrorsNeverContainValue(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name   string
		file   string // written to a temp config file, unless path is set
		path   string // an explicit path instead of file
		env    []string
		reason string
	}{
		{name: "file_not_found", path: filepath.Join(dir, "missing.yaml"), reason: reasonFileNotFound},
		{name: "cannot_read_file", path: dir, reason: reasonCannotRead},
		{name: "malformed_yaml", file: "log_level: [" + sentinel + "\n", reason: reasonMalformedYAML},
		{name: "malformed_yaml_no_line", file: "\tlog_level: " + sentinel + "\n", reason: reasonMalformedYAML},
		{name: "not_a_mapping", file: "- " + sentinel + "\n", reason: reasonNotMapping},
		{name: "unknown_key", file: "nope: " + sentinel + "\n", reason: reasonUnknownKey},
		{name: "duplicate_key", file: "log_level: info\nlog_level: " + sentinel + "\n", reason: reasonDuplicateKey},
		{name: "invalid_type", file: "log_level: [" + sentinel + "]\n", reason: reasonInvalidType},
		{name: "invalid_config", file: "log_level: !!int " + sentinel + "\n", reason: reasonInvalidConfig},
		{name: "multiple_documents", file: "log_level: info\n---\nlog_level: " + sentinel + "\n", reason: reasonMultipleDocuments},
		{name: "multiple_documents_malformed", file: "log_level: info\n---\n[" + sentinel + "\n", reason: reasonMultipleDocuments},
		{name: "invalid_duration_file", file: "shutdown_timeout: " + sentinel + "\n", reason: reasonInvalidDuration},
		{name: "invalid_duration_env", env: []string{"GATEWAY_SHUTDOWN_TIMEOUT", sentinel}, reason: reasonInvalidDuration},
		{name: "invalid_level_file", file: "log_level: " + sentinel + "\n", reason: reasonInvalidLevel},
		{name: "invalid_level_env", env: []string{"GATEWAY_LOG_LEVEL", sentinel}, reason: reasonInvalidLevel},
		{name: "invalid_address_file", file: "listen_addr: " + sentinel + "\n", reason: reasonInvalidAddress},
		{name: "invalid_address_env", env: []string{"GATEWAY_LISTEN_ADDR", sentinel}, reason: reasonInvalidAddress},
		{name: "invalid_url_file", file: "upstreams:\n  anthropic:\n    base_url: ftp://" + sentinel + "\n", reason: reasonInvalidURL},
		{name: "invalid_url_env", env: []string{"GATEWAY_UPSTREAMS_ANTHROPIC_BASE_URL", "http://" + sentinel}, reason: reasonInvalidURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := Options{Path: tc.path, LookupEnv: env(tc.env...), Upstreams: []UpstreamSpec{{Name: "anthropic", DefaultBaseURL: "https://api.anthropic.com"}}}
			if tc.file != "" {
				opts.Path = writeConfig(t, tc.file)
			}
			e := loadErr(t, opts)
			if e.Reason != tc.reason {
				t.Errorf("Reason = %q, want %q", e.Reason, tc.reason)
			}
			if s := e.Error() + e.Key + e.Source + e.Reason; strings.Contains(s, sentinel) {
				t.Errorf("error contains the value: %q", e.Error())
			}
		})
	}
}

func TestLoad_MalformedYAMLReportsLine(t *testing.T) {
	// The line is yaml.v3's own, passed through. For some errors it is the line
	// before the bad one (research Q4); this one reports the bad line.
	path := writeConfig(t, "log_level: info\n\nlisten_addr: a: b\n")
	e := loadErr(t, Options{Path: path})
	want := Error{Source: path, Reason: reasonMalformedYAML, Line: 3}
	if *e != want {
		t.Errorf("error = %+v, want %+v", *e, want)
	}

	// Some yaml.v3 syntax errors carry no line; the error then names only the file.
	path = writeConfig(t, "\tlog_level: info\n")
	e = loadErr(t, Options{Path: path})
	want = Error{Source: path, Reason: reasonMalformedYAML}
	if *e != want {
		t.Errorf("error = %+v, want %+v", *e, want)
	}
}

// Empty documents set no keys (research Q3).
func TestLoad_EmptyFile(t *testing.T) {
	for name, content := range map[string]string{
		"empty":        "",
		"comment_only": "# nothing here\n",
		"bare_marker":  "---\n",
		"null_values":  "log_level:\nshutdown_timeout: ~\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, content)
			cfg, src, err := Load(Options{Path: path})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cfg, Defaults()) || src.File != path {
				t.Errorf("Load() = %+v from %q, want defaults from %q", cfg, src.File, path)
			}
		})
	}
}

// config.example.yaml documents the defaults, so it must load as them.
func TestLoad_ExampleFileIsDefaults(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, string(b))
	specs := []UpstreamSpec{{Name: "anthropic", DefaultBaseURL: "https://api.anthropic.com"}}
	cfg, src, err := Load(Options{Path: path, Upstreams: specs})
	if err != nil {
		t.Fatal(err)
	}
	want := Defaults()
	want.Upstreams = map[string]Upstream{"anthropic": {
		BaseURL:               &url.URL{Scheme: "https", Host: "api.anthropic.com"},
		ConnectTimeout:        10 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Minute,
	}}
	if !reflect.DeepEqual(cfg, want) || src.File != path {
		t.Errorf("Load() = %+v from %q, want defaults from %q", cfg, src.File, path)
	}
	// Every key is shown, so the example also documents each env name. A nested key
	// is shown by its last segment, under its block.
	for _, s := range settingsFor(specs) {
		leaf := s.key[strings.LastIndex(s.key, ".")+1:]
		if !strings.Contains(string(b), leaf+":") || !strings.Contains(string(b), envName(s.key)) {
			t.Errorf("config.example.yaml does not show %s and %s", leaf, envName(s.key))
		}
	}
}

// checkInvalid asserts that value gives reason for key, from the file and from env.
func checkInvalid(t *testing.T, key, value, reason string) {
	t.Helper()
	path := writeConfig(t, key+": \""+value+"\"\n")
	if e := loadErr(t, Options{Path: path}); *e != (Error{Key: key, Source: path, Reason: reason, Line: 1}) {
		t.Errorf("file %q: error = %+v", value, *e)
	}
	name := envName(key)
	if e := loadErr(t, Options{LookupEnv: env(name, value)}); *e != (Error{Key: key, Source: name, Reason: reason}) {
		t.Errorf("env %q: error = %+v", value, *e)
	}
}

// checkValid asserts that value loads for key, from the file and from env.
func checkValid(t *testing.T, key, value string) {
	t.Helper()
	path := writeConfig(t, key+": \""+value+"\"\n")
	if _, _, err := Load(Options{Path: path}); err != nil {
		t.Errorf("file %q: %v", value, err)
	}
	if _, _, err := Load(Options{LookupEnv: env(envName(key), value)}); err != nil {
		t.Errorf("env %q: %v", value, err)
	}
}

func TestLoad_InvalidDuration(t *testing.T) {
	for _, v := range []string{"abc", "0s", "-5s"} {
		checkInvalid(t, "shutdown_timeout", v, reasonInvalidDuration)
	}
}

func TestLoad_InvalidAddress(t *testing.T) {
	for _, v := range []string{"localhost", ":7197", ":http", "127.0.0.1:-1", "127.0.0.1:65536", "127.0.0.1:99999"} {
		checkInvalid(t, "listen_addr", v, reasonInvalidAddress)
	}
	// An empty env var is unset, so an empty address can only come from the file.
	path := writeConfig(t, "listen_addr: \"\"\n")
	if e := loadErr(t, Options{Path: path}); e.Reason != reasonInvalidAddress {
		t.Errorf("empty address: Reason = %q, want %q", e.Reason, reasonInvalidAddress)
	}
	for _, v := range []string{"127.0.0.1:0", "0.0.0.0:7197", "[::1]:7197", "0.0.0.0:65535"} {
		checkValid(t, "listen_addr", v)
	}
}

func TestError_Message(t *testing.T) {
	cases := map[string]Error{
		"config: /etc/gw.yaml:3: log_level: invalid level":    {Key: "log_level", Source: "/etc/gw.yaml", Reason: reasonInvalidLevel, Line: 3},
		"config: GATEWAY_LOG_LEVEL: log_level: invalid level": {Key: "log_level", Source: "GATEWAY_LOG_LEVEL", Reason: reasonInvalidLevel},
		"config: /etc/gw.yaml: malformed yaml":                {Source: "/etc/gw.yaml", Reason: reasonMalformedYAML},
		"config: /etc/gw.yaml:2: listen_adr: unknown key":     {Key: "listen_adr", Source: "/etc/gw.yaml", Reason: reasonUnknownKey, Line: 2},
	}
	for want, e := range cases {
		if got := e.Error(); got != want {
			t.Errorf("Error() = %q, want %q", got, want)
		}
	}
}
