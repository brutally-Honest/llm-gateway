package core

import (
	"context"
	"errors"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/config"
	"github.com/brutally-honest/llm-gateway/internal/logging"
)

// forwardingHeaders are the client's own forwarding headers. ReverseProxy deletes
// them before Rewrite runs; the gateway forwards everything that is not hop-by-hop,
// so Rewrite puts them back. It never adds one (research Q5).
var forwardingHeaders = []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"}

// Proxy forwards one adapter's requests to its upstream and streams the response
// back. It is protocol-agnostic: everything protocol-shaped comes from the Adapter.
type Proxy struct {
	rp       *httputil.ReverseProxy
	adapter  Adapter
	identify func(*http.Request) string
}

// NewProxy builds the proxy for adapter a in front of up. identify names the client
// that sent a request.
func NewProxy(a Adapter, up config.Upstream, identify func(*http.Request) string, log *zap.Logger) *Proxy {
	prefix := a.Prefix()
	base := up.BaseURL
	p := &Proxy{adapter: a, identify: identify}
	p.rp = &httputil.ReverseProxy{
		Transport: newTransport(up),
		Rewrite: func(pr *httputil.ProxyRequest) {
			rewrite(pr, prefix, base)
			watchRequestBody(pr)
		},
		ModifyResponse: modifyResponse,
		ErrorHandler:   p.handleError,
		// The one line ReverseProxy writes itself, on a failure after headers, is JSON.
		ErrorLog: logging.StdLog(log),
		// Flush after every write, whatever the content type or length: a stream
		// reaches the client as upstream sends it.
		FlushInterval: -1,
	}
	return p
}

// The reasons for the errors the gateway creates. Each is sent as x-gateway-error,
// logged as gateway_error, and handed to the adapter for its error envelope.
const (
	reasonClientBody          = "client_body"
	reasonUpstreamTimeout     = "upstream_timeout"
	reasonUpstreamUnreachable = "upstream_unreachable"
)

// statusClientClosedRequest is the status logged, and sent if anyone still reads it,
// when the client left before upstream answered (research Q13). It is not an error
// the gateway created, so it carries no body and no reason.
const statusClientClosedRequest = 499

// handleError answers a request that got no upstream response headers. ReverseProxy
// calls it only before any header is written; after them it can only abort, and the
// access log's Meta.Settle tells whose fault that was. It never logs and never formats
// err, which can carry the upstream address.
func (p *Proxy) handleError(w http.ResponseWriter, r *http.Request, err error) {
	m := MetaFrom(r.Context())
	status, reason := classify(r.Context(), m, err)
	if reason == "" {
		if m != nil {
			m.ClientDisconnected = true
		}
		w.WriteHeader(status)
		return
	}
	contentType, body := p.adapter.ErrorBody(reason)
	// Set, not Add: ServeHTTP left an empty Content-Type entry on the writer.
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Gateway-Error", reason)
	if m != nil {
		m.GatewayError = reason
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// classify maps a failure before response headers to its status and reason; the
// first match wins. ctx is the inbound request's context. A client that has left
// gets 499 and no reason: the gateway created no error. That includes a body short
// of its Content-Length, because net/http cancels the context on the connection's
// EOF (research Q19). A failed read of the client's own body on a live connection,
// such as broken chunked framing, is the client's fault whatever the transport made
// of it (research Q11). Then a timeout dialling, in the TLS handshake or awaiting
// headers; then anything else, upstream's fault.
func classify(ctx context.Context, m *Meta, err error) (int, string) {
	var netErr net.Error
	switch {
	case ctx.Err() != nil:
		return statusClientClosedRequest, ""
	case m != nil && m.requestBodyErr() != nil:
		return http.StatusBadRequest, reasonClientBody
	case errors.As(err, &netErr) && netErr.Timeout():
		return http.StatusGatewayTimeout, reasonUpstreamTimeout
	default:
		return http.StatusBadGateway, reasonUpstreamUnreachable
	}
}

// watchRequestBody wraps the outbound body, after rewrite's five steps, so a failed
// read of the client's body can be told from an upstream failure. The watcher changes
// no byte, and ContentLength is left as it is.
func watchRequestBody(pr *httputil.ProxyRequest) {
	if pr.Out.Body == nil {
		return
	}
	if m := MetaFrom(pr.Out.Context()); m != nil {
		pr.Out.Body = m.watchRequestBody(pr.Out.Body)
	}
}

// ServeHTTP labels the request for the access log, then forwards it.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if m := MetaFrom(r.Context()); m != nil {
		m.Protocol = p.adapter.Name()
		m.Client = p.identify(r)
		m.Auth = p.adapter.AuthKind(r.Header)
		m.start = time.Now()
	}
	// net/http sniffs a Content-Type for a response that has none when its first
	// body write goes out with the headers. ReverseProxy's initial header flush
	// usually wins that race, but not always; a present but empty entry stops the
	// sniff outright, so a response upstream sent without one arrives without one.
	// ReverseProxy adds upstream's own to this entry when there is one.
	if _, ok := w.Header()["Content-Type"]; !ok {
		w.Header()["Content-Type"] = nil
	}
	p.rp.ServeHTTP(w, r)
}

// modifyResponse runs after ReverseProxy has removed the hop-by-hop headers. It
// changes nothing else about the response: no status, no other header, no body byte.
func modifyResponse(res *http.Response) error {
	stripProxyHeaders(res.Header)
	// ReverseProxy adds upstream's headers to the writer's, where 000's requestID has
	// already set the gateway's X-Request-Id; deleting upstream's leaves exactly one
	// (research Q6). A differently named request-id is not touched.
	res.Header.Del("X-Request-Id")
	if m := MetaFrom(res.Request.Context()); m != nil {
		m.Stream = isEventStream(res.Header.Get("Content-Type"))
		m.TTFB = time.Since(m.start)
		m.HasTTFB = true
		res.Body = m.watchResponseBody(res.Body)
	}
	return nil
}

// isEventStream reports whether contentType is the generic server-sent-events media
// type, parameters aside.
func isEventStream(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "text/event-stream"
}

// newTransport is built field by field rather than cloned from http.DefaultTransport,
// which a test in the same process could have replaced. There is no whole-request
// timeout anywhere: a stream runs as long as upstream sends.
func newTransport(up config.Upstream) *http.Transport {
	dialer := &net.Dialer{Timeout: up.ConnectTimeout, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:       http.ProxyFromEnvironment, // research Q9
		DialContext: dialer.DialContext,
		// Accept-Encoding goes upstream as sent, and a compressed body comes back as
		// sent: the gateway never inflates it.
		DisableCompression:    true,
		TLSHandshakeTimeout:   up.TLSHandshakeTimeout,
		ResponseHeaderTimeout: up.ResponseHeaderTimeout,
		// A custom DialContext turns HTTP/2 off unless this is set.
		ForceAttemptHTTP2: true,
		// The stdlib default of 2 idle connections per host would make parallel
		// requests re-handshake TLS constantly.
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// rewrite turns the inbound request into the upstream one, changing only what
// forwarding requires. ReverseProxy has already removed the hop-by-hop headers,
// including those Connection names.
func rewrite(pr *httputil.ProxyRequest, prefix string, base *url.URL) {
	// 1. Strip the adapter prefix. RawPath too, so an escaped path keeps its escapes.
	pr.Out.URL.Path = strings.TrimPrefix(pr.Out.URL.Path, prefix)
	if pr.Out.URL.RawPath != "" {
		pr.Out.URL.RawPath = strings.TrimPrefix(pr.Out.URL.RawPath, prefix)
	}
	// 2. Scheme, host and base path from base_url; Out.Host is cleared so the upstream
	// host is sent. Never SetXForwarded.
	pr.SetURL(base)
	// 3. ReverseProxy drops query parameters it cannot parse; forward the query as sent.
	pr.Out.URL.RawQuery = pr.In.URL.RawQuery
	// 4. The client's forwarding headers, unless its Connection header made them
	// hop-by-hop.
	listed := connectionTokens(pr.In.Header)
	for _, name := range forwardingHeaders {
		if v, ok := pr.In.Header[name]; ok && !slices.Contains(listed, name) {
			pr.Out.Header[name] = slices.Clone(v)
		}
	}
	// 5. ReverseProxy re-adds Te: trailers, and Connection and Upgrade for a protocol
	// switch, after stripping them; all three are hop-by-hop. So is any Proxy-* header,
	// of which ReverseProxy knows only two.
	pr.Out.Header.Del("Te")
	pr.Out.Header.Del("Connection")
	pr.Out.Header.Del("Upgrade")
	stripProxyHeaders(pr.Out.Header)
}

// connectionTokens are the header names h's Connection header lists, canonicalised.
func connectionTokens(h http.Header) []string {
	var names []string
	for _, v := range h.Values("Connection") {
		for _, tok := range strings.Split(v, ",") {
			if tok = strings.TrimSpace(tok); tok != "" {
				names = append(names, http.CanonicalHeaderKey(tok))
			}
		}
	}
	return names
}

// stripProxyHeaders deletes every Proxy-* header, which the spec lists as hop-by-hop.
func stripProxyHeaders(h http.Header) {
	for name := range h {
		if strings.HasPrefix(http.CanonicalHeaderKey(name), "Proxy-") {
			delete(h, name)
		}
	}
}
