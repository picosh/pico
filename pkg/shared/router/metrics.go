package router

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// UserMetricsOpts tells UserMetrics who a request belongs to when no
// handler called SetUser, such as a response served from a cache.
type UserMetricsOpts struct {
	// Owner names the user and resource a request is for, read from the
	// host without checking that either exists.
	Owner func(r *http.Request) (user, resource string)
	// TrustSuccess counts an Owner as real once it returns a success, for
	// a proxy that can't look users up. Only safe when every route on a
	// site's host fails for a site that doesn't exist.
	TrustSuccess bool
}

// UserMetrics counts HTTP requests, latency and response bytes for each
// site owner. Requests and bytes also carry the resource they were for,
// such as a pgs project, which the latency histogram leaves out to keep its
// series down. Every app shares the metric names and sets the app label, so
// queries can aggregate across services.
type UserMetrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	bytes    *prometheus.CounterVec
	opts     UserMetricsOpts
	// known holds the users and resources a handler or a successful
	// response confirmed. A name from the Host header alone never becomes a
	// label, so made-up subdomains can't add series.
	known *lru.Cache[string, struct{}]
}

func NewUserMetrics(app string, reg prometheus.Registerer, opts UserMetricsOpts) *UserMetrics {
	auto := promauto.With(reg)
	known, _ := lru.New[string, struct{}](16384)
	return &UserMetrics{
		requests: auto.NewCounterVec(prometheus.CounterOpts{
			Namespace:   "pico",
			Subsystem:   "http",
			ConstLabels: prometheus.Labels{"app": app},
			Name:        "requests_total",
			Help:        "Number of HTTP requests handled.",
		}, []string{"user", "resource", "status", "method"}),
		duration: auto.NewHistogramVec(prometheus.HistogramOpts{
			Namespace:   "pico",
			Subsystem:   "http",
			ConstLabels: prometheus.Labels{"app": app},
			Name:        "request_duration_seconds",
			Help:        "Duration of HTTP requests in seconds.",
			Buckets:     prometheus.DefBuckets,
		}, []string{"user", "status", "method"}),
		bytes: auto.NewCounterVec(prometheus.CounterOpts{
			Namespace:   "pico",
			Subsystem:   "http",
			ConstLabels: prometheus.Labels{"app": app},
			Name:        "response_bytes_total",
			Help:        "Number of response body bytes sent.",
		}, []string{"user", "resource"}),
		opts:  opts,
		known: known,
	}
}

type owner struct {
	user     string
	resource string
}

type ctxOwnerKey struct{}

// SetUser records name as the user a request belongs to. Handlers call it
// once they've found the user in the database.
func SetUser(r *http.Request, name string) {
	if o, ok := r.Context().Value(ctxOwnerKey{}).(*owner); ok {
		o.user = name
	}
}

// SetResource records name as what a request is for within the user's
// account, such as a pgs project. Handlers call it alongside SetUser once
// they've found it.
func SetResource(r *http.Request, name string) {
	if o, ok := r.Context().Value(ctxOwnerKey{}).(*owner); ok {
		o.resource = name
	}
}

func (m *UserMetrics) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var o owner
		mw := &metricsWriter{ResponseWriter: w}
		next.ServeHTTP(mw, r.WithContext(context.WithValue(r.Context(), ctxOwnerKey{}, &o)))

		status := mw.Status()
		user, resource := m.resolve(r, o, status)
		m.Observe(user, resource, status, r.Method, time.Since(start), mw.bytes)
	})
}

// Observe records one request for servers that can't use Handler, such as
// tuns's gin logger. user and resource must already be confirmed to
// exist, and resource may be empty. A negative size, which gin reports when
// nothing was written, counts as zero bytes.
func (m *UserMetrics) Observe(user, resource string, status int, method string, elapsed time.Duration, bytes int64) {
	if user == "" {
		resource = ""
	}
	code, verb := strconv.Itoa(status), metricMethod(method)
	m.requests.WithLabelValues(user, resource, code, verb).Inc()
	m.duration.WithLabelValues(user, code, verb).Observe(elapsed.Seconds())
	m.bytes.WithLabelValues(user, resource).Add(float64(max(bytes, 0)))
}

func (m *UserMetrics) resolve(r *http.Request, o owner, status int) (string, string) {
	var hostUser, hostResource string
	if m.opts.Owner != nil {
		hostUser, hostResource = m.opts.Owner(r)
	}
	trusted := m.opts.TrustSuccess && (status >= 200 && status < 300 || status == http.StatusNotModified)

	user := m.confirm(o.user, hostUser, trusted)
	if user == "" {
		return "", ""
	}
	// Resources are keyed under their owner, and user names can't hold a
	// slash.
	prefix := user + "/"
	set, fromHost := "", ""
	if o.resource != "" {
		set = prefix + o.resource
	}
	if hostUser == user && hostResource != "" {
		fromHost = prefix + hostResource
	}
	resource := strings.TrimPrefix(m.confirm(set, fromHost, trusted), prefix)
	return user, resource
}

// confirm returns the name a handler set, or else the name from the host
// once it's known or trusted.
func (m *UserMetrics) confirm(set, fromHost string, trusted bool) string {
	if set != "" {
		m.known.Add(set, struct{}{})
		return set
	}
	if fromHost == "" {
		return ""
	}
	if m.known.Contains(fromHost) {
		return fromHost
	}
	if trusted {
		m.known.Add(fromHost, struct{}{})
		return fromHost
	}
	return ""
}

// metricMethod keeps clients from adding series with made-up methods.
func metricMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions, http.MethodConnect, http.MethodTrace:
		return method
	}
	return "OTHER"
}

type metricsWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *metricsWriter) WriteHeader(code int) {
	// Informational responses like 103 Early Hints precede the real status.
	if w.status == 0 && (code >= 200 || code == http.StatusSwitchingProtocols) {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *metricsWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *metricsWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *metricsWriter) Flush() {
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Hijack lets websockets upgrade through the middleware.
func (w *metricsWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil && w.status == 0 {
		w.status = http.StatusSwitchingProtocols
	}
	return conn, rw, err
}

func (w *metricsWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
