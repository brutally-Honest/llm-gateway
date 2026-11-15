// Package observability is the gateway watching itself (spec 021): Prometheus metrics
// about its own health and, when turned on, OpenTelemetry spans for a proxied call. It
// describes the gateway, never the traffic: no label is a session, exchange or request
// id, a path, a host or a model, and no span attribute is content or a header.
package observability

import (
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// The listeners a request is counted under.
const (
	ListenerAPI = "api"
	ListenerUI  = "ui"
)

// The kinds of upstream error (AC3).
const (
	UpstreamTimeout     = "timeout"
	UpstreamUnreachable = "unreachable"
	UpstreamAborted     = "aborted"
	UpstreamRateLimited = "rate_limited"
	UpstreamStatus5xx   = "status_5xx"
)

// The store calls a write is timed under (AC4).
const (
	StoreOpExchange = "exchange"
	StoreOpParse    = "parse"
)

// latencyBuckets span 1 ms to about 5 minutes: a model call's total time can be minutes.
var latencyBuckets = []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300}

// fastBuckets span 10 µs to about 1 s: work the gateway does itself.
var fastBuckets = []float64{.00001, .000025, .00005, .0001, .00025, .0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1}

// Metrics is the gateway's own metrics, on a registry of their own. A nil *Metrics is
// metrics off: every method is a no-op on it, so tests can leave it out.
type Metrics struct {
	reg *prometheus.Registry

	requests      *prometheus.CounterVec
	requestTime   *prometheus.HistogramVec
	overhead      *prometheus.HistogramVec
	upstreamTTFB  *prometheus.HistogramVec
	upstreamTotal *prometheus.HistogramVec
	upstreamErrs  *prometheus.CounterVec
	storeWrite    *prometheus.HistogramVec
	viewBuild     *prometheus.HistogramVec
	warmup        prometheus.Gauge
}

// NewMetrics registers every metric, build info for version and the Go runtime and
// process collectors.
func NewMetrics(version string) *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		reg: reg,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_http_requests_total",
			Help: "Requests answered, by listener, status class and whether they were proxied to a provider.",
		}, []string{"listener", "status_class", "proxied"}),
		requestTime: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gateway_http_request_duration_seconds",
			Help:    "Request time from receipt to the end of the response, by listener, status class and proxied.",
			Buckets: latencyBuckets,
		}, []string{"listener", "status_class", "proxied"}),
		overhead: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gateway_proxy_overhead_seconds",
			Help:    "Time a proxied call spends in the gateway before it is sent upstream.",
			Buckets: fastBuckets,
		}, []string{"protocol"}),
		upstreamTTFB: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gateway_upstream_ttfb_seconds",
			Help:    "Time from the proxy taking a call to upstream's response headers.",
			Buckets: latencyBuckets,
		}, []string{"protocol", "stream"}),
		upstreamTotal: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gateway_upstream_duration_seconds",
			Help:    "Time from the proxy taking a call to the end of upstream's response.",
			Buckets: latencyBuckets,
		}, []string{"protocol", "stream"}),
		upstreamErrs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_upstream_errors_total",
			Help: "Upstream failures by kind: timeout, unreachable, aborted mid-response, rate_limited (429), status_5xx.",
		}, []string{"protocol", "kind"}),
		storeWrite: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gateway_store_write_seconds",
			Help:    "Time of one capture store call, by op (exchange, parse) and outcome (ok, error).",
			Buckets: fastBuckets,
		}, []string{"op", "outcome"}),
		viewBuild: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gateway_ui_view_build_seconds",
			Help:    "Time to serve a session view: cached (a view cache hit) or cold (built).",
			Buckets: latencyBuckets,
		}, []string{"cache"}),
		warmup: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "gateway_ui_warmup_seconds",
			Help: "How long this start's UI warm-up (the sessions list's first page) took.",
		}),
	}
	buildInfo := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "gateway_build_info",
		Help:        "Always 1; the labels name the running build.",
		ConstLabels: prometheus.Labels{"version": version, "go_version": runtime.Version()},
	})
	buildInfo.Set(1)
	reg.MustRegister(m.requests, m.requestTime, m.overhead, m.upstreamTTFB, m.upstreamTotal,
		m.upstreamErrs, m.storeWrite, m.viewBuild, m.warmup, buildInfo,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// Register adds a collector, for metrics read from a component when scraped (queue
// depth, open tunnels). It panics on a duplicate, which is a wiring bug.
func (m *Metrics) Register(c prometheus.Collector) {
	if m == nil {
		return
	}
	m.reg.MustRegister(c)
}

// GaugeFunc registers a gauge whose value f reads at scrape time.
func (m *Metrics) GaugeFunc(name, help string, f func() float64) {
	m.Register(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help}, f))
}

// CounterFunc registers a counter whose value f reads at scrape time; labels are its
// fixed label values, so one name can be registered once per value set.
func (m *Metrics) CounterFunc(name, help string, labels map[string]string, f func() float64) {
	m.Register(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: name, Help: help, ConstLabels: labels}, f))
}

// Request counts one answered request.
func (m *Metrics) Request(listener string, status int, proxied bool, d time.Duration) {
	if m == nil {
		return
	}
	class, p := StatusClass(status), strconv.FormatBool(proxied)
	m.requests.WithLabelValues(listener, class, p).Inc()
	m.requestTime.WithLabelValues(listener, class, p).Observe(d.Seconds())
}

// Upstream is what one proxied call learned about its upstream.
type Upstream struct {
	Protocol string
	Stream   bool
	// Answered is true once upstream's response headers arrived; TTFB and Total are
	// observed only then.
	Answered    bool
	TTFB, Total time.Duration
	Overhead    time.Duration
	HasOverhead bool   // the call was sent upstream
	ErrorKind   string // "" or one of the Upstream* kinds
	Status      int
}

// Proxied records one proxied call's upstream timings and error.
func (m *Metrics) Proxied(u Upstream) {
	if m == nil {
		return
	}
	stream := strconv.FormatBool(u.Stream)
	if u.HasOverhead {
		m.overhead.WithLabelValues(u.Protocol).Observe(u.Overhead.Seconds())
	}
	if u.Answered {
		m.upstreamTTFB.WithLabelValues(u.Protocol, stream).Observe(u.TTFB.Seconds())
		m.upstreamTotal.WithLabelValues(u.Protocol, stream).Observe(u.Total.Seconds())
	}
	if u.ErrorKind != "" {
		m.upstreamErrs.WithLabelValues(u.Protocol, u.ErrorKind).Inc()
	}
}

// StoreWrite times one capture store call.
func (m *Metrics) StoreWrite(op string, ok bool, d time.Duration) {
	if m == nil {
		return
	}
	outcome := "ok"
	if !ok {
		outcome = "error"
	}
	m.storeWrite.WithLabelValues(op, outcome).Observe(d.Seconds())
}

// ViewBuild times one session view served by the UI.
func (m *Metrics) ViewBuild(cached bool, d time.Duration) {
	if m == nil {
		return
	}
	cache := "cold"
	if cached {
		cache = "cached"
	}
	m.viewBuild.WithLabelValues(cache).Observe(d.Seconds())
}

// Warmup records this start's UI warm-up time.
func (m *Metrics) Warmup(d time.Duration) {
	if m == nil {
		return
	}
	m.warmup.Set(d.Seconds())
}

// StatusClass is the status's class, "2xx" and so on, or "none" for 0 (nothing was
// written) or a value outside 100–599.
func StatusClass(status int) string {
	if status < 100 || status > 599 {
		return "none"
	}
	return strconv.Itoa(status/100) + "xx"
}
