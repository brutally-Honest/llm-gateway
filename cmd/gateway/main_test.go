package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// buildHome is HOME as the test process got it. Only the go toolchain run by
// binary_test.go sees it, so its build and module caches are the real ones; the
// gateway never does.
var buildHome string

// TestMain is the default data-dir guard (spec 002, Do): HOME and XDG_DATA_HOME point
// at a temp dir for the whole package, and the package fails if a capture store
// appears under it. Every test is meant to set its own capture.dir, so a store there
// means one forgot and would have written into the real ~/.local/share.
func TestMain(m *testing.M) {
	os.Exit(guardDataDir(m))
}

func guardDataDir(m *testing.M) int {
	buildHome = os.Getenv("HOME")
	home, err := os.MkdirTemp("", "gateway-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "data-dir guard:", err)
		return 1
	}
	defer func() { _ = os.RemoveAll(home) }()
	for k, v := range map[string]string{"HOME": home, "XDG_DATA_HOME": home} {
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintln(os.Stderr, "data-dir guard:", err)
			return 1
		}
	}

	code := m.Run()
	for _, dir := range []string{
		filepath.Join(home, "llm-gateway"),                    // $XDG_DATA_HOME/llm-gateway
		filepath.Join(home, ".local", "share", "llm-gateway"), // $HOME/.local/share/llm-gateway
	} {
		if _, err := os.Lstat(dir); err == nil {
			fmt.Fprintf(os.Stderr, "data-dir guard: a test wrote the default capture dir %s; set capture.dir to a temp dir\n", dir)
			code = 1
		}
	}
	return code
}
