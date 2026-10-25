package router

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strconv"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// UserMetricsOpts tells UserMetrics who a request belongs to when no
// handler called SetUser, such as a response served from a cache.
type UserMetricsOpts struct {
	// SiteUser names the owner of the site a request is for, without
	// checking that the user exists.
	SiteUser func(r *http.Request) string
	// TrustSuccess counts a SiteUser as real once its site returns a
	// success, for a proxy that can't look users up. Only safe when every
	// route on a site's host fails for a site that doesn't exist.
	TrustSuccess bool
}

// UserMetrics counts HTTP requests, latency and response bytes for each
// site owner. Every app shares the metric names and sets the app label, so
// queries can aggregate across services.
type UserMetrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	bytes    *prometheus.CounterVec
	opts     UserMetricsOpts
	// known holds the users a handler or a successful response confirmed.
	// A name from the Host header alone never becomes a label, so made-up
	// subdomains can't add series.
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
		}, []string{"user", "status", "method"}),
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
		}, []string{"user"}),
		opts:  opts,
		known: known,
	}
}

type ctxUserKey struct{}

// SetUser records name as the user a request belongs to. Handlers call it
// once they've found the user in the database.
func SetUser(r *http.Request, name string) {
	if user, ok := r.Context().Value(ctxUserKey{}).(*string); ok {
		*user = name
	}
}

func (m *UserMetrics) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var user string
		mw := &metricsWriter{ResponseWriter: w}
		next.ServeHTTP(mw, r.WithContext(context.WithValue(r.Context(), ctxUserKey{}, &user)))

		status := mw.Status()
		user = m.resolveUser(r, user, status)
		labels := []string{user, strconv.Itoa(status), metricMethod(r.Method)}
		m.requests.WithLabelValues(labels...).Inc()
		m.duration.WithLabelValues(labels...).Observe(time.Since(start).Seconds())
		m.bytes.WithLabelValues(user).Add(float64(mw.bytes))
	})
}

func (m *UserMetrics) resolveUser(r *http.Request, user string, status int) string {
	if user != "" {
		m.known.Add(user, struct{}{})
		return user
	}
	if m.opts.SiteUser == nil {
		return ""
	}
	user = m.opts.SiteUser(r)
	if user == "" {
		return ""
	}
	if m.known.Contains(user) {
		return user
	}
	if m.opts.TrustSuccess && (status >= 200 && status < 300 || status == http.StatusNotModified) {
		m.known.Add(user, struct{}{})
		return user
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
