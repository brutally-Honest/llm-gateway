package main

import (
	"net"
	"testing"
)

// freeAddr is a 127.0.0.1 address with a port that was free a moment ago. Tests that need
// a second listener use it instead of another loopback address such as 127.0.0.2, which
// macOS does not have, and the config refuses two listeners on the same "127.0.0.1:0"
// (spec 024 AC6, macos-v1.md B6).
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}
