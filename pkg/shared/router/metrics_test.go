package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// metricValue reads a counter, or a histogram's count, from reg.
func metricValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
	metrics:
		for _, m := range family.GetMetric() {
			for _, l := range m.GetLabel() {
				if want, ok := labels[l.GetName()]; ok && want != l.GetValue() {
					continue metrics
				}
			}
			if h := m.GetHistogram(); h != nil {
				return float64(h.GetSampleCount())
			}
			return m.GetCounter().GetValue()
		}
	}
	return 0
}

func serve(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestUserMetricsSetUser(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewUserMetrics("test", reg, UserMetricsOpts{})
	h := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetUser(r, "alice")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello"))
	}))

	serve(h, http.MethodPost, "/")

	labels := map[string]string{"app": "test", "user": "alice", "status": "201", "method": "POST"}
	if got := metricValue(t, reg, "pico_http_requests_total", labels); got != 1 {
		t.Errorf("requests = %v, want 1", got)
	}
	if got := metricValue(t, reg, "pico_http_request_duration_seconds", labels); got != 1 {
		t.Errorf("duration samples = %v, want 1", got)
	}
	if got := metricValue(t, reg, "pico_http_response_bytes_total", map[string]string{"user": "alice"}); got != 5 {
		t.Errorf("bytes = %v, want 5", got)
	}
}

func TestUserMetricsSiteUser(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewUserMetrics("test", reg, UserMetricsOpts{
		SiteUser: func(r *http.Request) string { return strings.Split(r.Host, ".")[0] },
	})
	// The handler confirms alice on her first request; later ones stand in
	// for cache hits that never reach it.
	confirmed := false
	h := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !confirmed && r.Host == "alice.example.com" {
			SetUser(r, "alice")
			confirmed = true
		}
	}))

	serve(h, http.MethodGet, "http://ghost.example.com/")
	serve(h, http.MethodGet, "http://alice.example.com/")
	serve(h, http.MethodGet, "http://alice.example.com/")

	if got := metricValue(t, reg, "pico_http_requests_total", map[string]string{"user": "alice"}); got != 2 {
		t.Errorf("alice requests = %v, want 2", got)
	}
	if got := metricValue(t, reg, "pico_http_requests_total", map[string]string{"user": ""}); got != 1 {
		t.Errorf("unlabeled requests = %v, want 1", got)
	}
	if got := metricValue(t, reg, "pico_http_requests_total", map[string]string{"user": "ghost"}); got != 0 {
		t.Errorf("ghost requests = %v, want 0", got)
	}
}

func TestUserMetricsTrustSuccess(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewUserMetrics("test", reg, UserMetricsOpts{
		SiteUser:     func(r *http.Request) string { return strings.Split(r.Host, ".")[0] },
		TrustSuccess: true,
	})
	h := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "ghost.example.com" {
			http.NotFound(w, r)
		}
	}))

	serve(h, http.MethodGet, "http://bob.example.com/")
	serve(h, http.MethodGet, "http://ghost.example.com/")

	if got := metricValue(t, reg, "pico_http_requests_total", map[string]string{"user": "bob", "status": "200"}); got != 1 {
		t.Errorf("bob requests = %v, want 1", got)
	}
	if got := metricValue(t, reg, "pico_http_requests_total", map[string]string{"user": "", "status": "404"}); got != 1 {
		t.Errorf("unlabeled 404s = %v, want 1", got)
	}
}

func TestUserMetricsMethod(t *testing.T) {
	reg := prometheus.NewRegistry()
	h := NewUserMetrics("test", reg, UserMetricsOpts{}).Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	serve(h, "MADEUP", "/")

	if got := metricValue(t, reg, "pico_http_requests_total", map[string]string{"method": "OTHER"}); got != 1 {
		t.Errorf("OTHER requests = %v, want 1", got)
	}
}

func TestUserMetricsHijack(t *testing.T) {
	reg := prometheus.NewRegistry()
	h := NewUserMetrics("test", reg, UserMetricsOpts{}).Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("writer does not implement http.Hijacker")
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n\r\n")
		_ = rw.Flush()
		_ = conn.Close()
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	_ = resp.Body.Close()

	if got := metricValue(t, reg, "pico_http_requests_total", map[string]string{"status": "101"}); got != 1 {
		t.Errorf("101 requests = %v, want 1", got)
	}
}
