package main

import (
	"math/rand/v2"
	"net"
	"strconv"
	"testing"
)

// Ports freeAddr picks from: below the kernel's ephemeral range on Linux (32768–60999)
// and macOS (49152–65535), so a listener another test opens on port 0 can never be given
// the same port in the moment between freeAddr closing it and the gateway binding it.
const (
	freePortLow  = 20000
	freePortHigh = 32767
)

// freeAddr is a 127.0.0.1 address with a free port. Tests that need a second listener use
// it instead of another loopback address such as 127.0.0.2, which macOS does not have;
// the config refuses two listeners on the same "127.0.0.1:0", and the gateway cannot be
// handed an open listener, so a port is picked and closed for the gateway to bind (spec
// 024 AC6, macos-v1.md B6, research D12).
func freeAddr(t *testing.T) string {
	t.Helper()
	for range 100 {
		port := freePortLow + rand.IntN(freePortHigh-freePortLow+1)
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			continue // taken: try another
		}
		addr := ln.Addr().String()
		if err := ln.Close(); err != nil {
			t.Fatal(err)
		}
		return addr
	}
	t.Fatalf("no free port in %d–%d after 100 tries", freePortLow, freePortHigh)
	return ""
}
