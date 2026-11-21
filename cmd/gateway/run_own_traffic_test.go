package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// B4 (spec 024 AC4): through the running gateway's forward proxy, a hook call to its own
// API listener and a tunnel to its UI listener are forwarded but not stored as network
// effects; a tunnel to a user's own local service is.
func TestRun_OwnListenerTrafficNotRecorded(t *testing.T) {
	dir := t.TempDir()
	up := jsonUpstream(t)
	g, api := startCapturing(t, captureEnv(dir, up, map[string]string{
		"GATEWAY_UI_LISTEN_ADDR":                   freeAddr(t),
		"GATEWAY_EFFECTS_NETWORK_HTTP_PROXY_ADDR":  freeAddr(t),
		"GATEWAY_EFFECTS_NETWORK_SOCKS_PROXY_ADDR": freeAddr(t),
	}), nil)
	proxy := proxyAddr(g)

	pu, _ := url.Parse("http://" + proxy)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(pu)}}
	res, err := client.Get(api + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("healthz through the proxy = %d", res.StatusCode)
	}
	client.CloseIdleConnections()

	tunnel := func(target string) {
		t.Helper()
		c, err := net.Dial("tcp", proxy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(c, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n"); err != nil {
			t.Fatal(err)
		}
		if status, err := bufio.NewReader(c).ReadString('\n'); err != nil || !strings.Contains(status, " 200 ") {
			t.Fatalf("CONNECT %s = %q, %v", target, status, err)
		}
		_ = c.Close()
	}
	tunnel(uiAddr(g))
	user := holdingTarget(t)
	tunnel(user)

	if code := g.stop(); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	effs, err := openReader(t, dir).WithContext(context.Background()).ListUnassigned(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(effs) != 1 || net.JoinHostPort(effs[0].Host, strconv.Itoa(effs[0].Port)) != user {
		t.Fatalf("stored connections = %+v, want only the user's service %s", effs, user)
	}
}
