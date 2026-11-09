package core

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// AC11: when the environment's proxy is one of the gateway's own proxy listeners, the
// upstream transport connects directly; any other proxy is still used.
func TestUpstream_SkipsOwnProxyListener(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "direct")
	}))
	t.Cleanup(upstream.Close)
	var ownHits atomic.Int32
	own := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ownHits.Add(1)
		_, _ = io.WriteString(w, "through the gateway's own proxy")
	}))
	t.Cleanup(own.Close)
	ownURL, _ := url.Parse(own.URL)

	for _, host := range []string{ownURL.Host, "localhost:" + ownURL.Port()} {
		proxyURL, _ := url.Parse("http://" + host)
		environmentProxy = http.ProxyURL(proxyURL)
		t.Cleanup(func() { environmentProxy = http.ProxyFromEnvironment })
		tr := newTransport(config.Upstream{ConnectTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second}, ownURL.Host)
		res, err := (&http.Client{Transport: tr}).Get(upstream.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if string(body) != "direct" {
			t.Errorf("proxy %s: body %q, want the upstream's answer", host, body)
		}
	}
	if n := ownHits.Load(); n != 0 {
		t.Errorf("own proxy got %d requests, want none", n)
	}

	other, _ := url.Parse("http://corp-proxy.example:3128")
	pick := skipOwnProxies(http.ProxyURL(other), []string{ownURL.Host})
	req, _ := http.NewRequest(http.MethodGet, upstream.URL, nil)
	if got, err := pick(req); err != nil || got == nil || got.Host != other.Host {
		t.Errorf("another proxy = %v, %v; want it kept", got, err)
	}
}
