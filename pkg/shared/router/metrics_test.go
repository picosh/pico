package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// metricValue sums the counters, or histogram counts, in reg that match
// labels.
func metricValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var total float64
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
				total += float64(h.GetSampleCount())
			} else {
				total += m.GetCounter().GetValue()
			}
		}
	}
	return total
}

// hostOwner reads "user-project.example.com" the way pgs reads its
// subdomains.
func hostOwner(r *http.Request) (string, string) {
	user, resource, _ := strings.Cut(strings.Split(r.Host, ".")[0], "-")
	return user, resource
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
	m := NewUserMetrics("test", reg, UserMetricsOpts{Owner: hostOwner})
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
		Owner:        hostOwner,
		TrustSuccess: true,
	})
	h := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "ghost.example.com" {
			http.NotFound(w, r)
		}
	}))

	serve(h, http.MethodGet, "http://bob-docs.example.com/")
	serve(h, http.MethodGet, "http://ghost.example.com/")

	if got := metricValue(t, reg, "pico_http_requests_total", map[string]string{"user": "bob", "status": "200"}); got != 1 {
		t.Errorf("bob requests = %v, want 1", got)
	}
	if got := metricValue(t, reg, "pico_http_requests_total", map[string]string{"user": "", "status": "404"}); got != 1 {
		t.Errorf("unlabeled 404s = %v, want 1", got)
	}
	if got := metricValue(t, reg, "pico_http_requests_total", map[string]string{"user": "bob", "resource": "docs"}); got != 1 {
		t.Errorf("bob/docs requests = %v, want 1", got)
	}
}

func TestUserMetricsSetResource(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewUserMetrics("test", reg, UserMetricsOpts{})
	h := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetUser(r, "alice")
		if r.URL.Path == "/blog" {
			SetResource(r, "blog")
		}
		_, _ = w.Write([]byte("hello"))
	}))

	serve(h, http.MethodGet, "/blog")
	serve(h, http.MethodGet, "/")

	labels := map[string]string{"app": "test", "user": "alice", "resource": "blog"}
	if got := metricValue(t, reg, "pico_http_requests_total", labels); got != 1 {
		t.Errorf("alice/blog requests = %v, want 1", got)
	}
	if got := metricValue(t, reg, "pico_http_response_bytes_total", labels); got != 5 {
		t.Errorf("alice/blog bytes = %v, want 5", got)
	}
	if got := metricValue(t, reg, "pico_http_requests_total", map[string]string{"user": "alice"}); got != 2 {
		t.Errorf("alice requests = %v, want 2", got)
	}
}

func TestUserMetricsResourceFromHost(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewUserMetrics("test", reg, UserMetricsOpts{Owner: hostOwner})
	// The handler confirms alice's blog on its first request; later ones
	// stand in for cache hits that never reach it.
	confirmed := false
	h := m.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !confirmed {
			SetUser(r, "alice")
			SetResource(r, "blog")
			confirmed = true
		}
	}))

	serve(h, http.MethodGet, "http://alice-blog.example.com/")
	serve(h, http.MethodGet, "http://alice-blog.example.com/")
	serve(h, http.MethodGet, "http://alice-ghost.example.com/")

	if got := metricValue(t, reg, "pico_http_requests_total", map[string]string{"user": "alice", "resource": "blog"}); got != 2 {
		t.Errorf("alice/blog requests = %v, want 2", got)
	}
	if got := metricValue(t, reg, "pico_http_requests_total", map[string]string{"user": "alice", "resource": ""}); got != 1 {
		t.Errorf("alice requests without a resource = %v, want 1", got)
	}
	if got := metricValue(t, reg, "pico_http_requests_total", map[string]string{"user": "alice"}); got != 3 {
		t.Errorf("alice requests = %v, want 3", got)
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

func TestUserMetricsObserve(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewUserMetrics("test", reg, UserMetricsOpts{})

	m.Observe("carol", "app", http.StatusOK, http.MethodGet, 0, 7)
	m.Observe("carol", "", http.StatusNotModified, "MADEUP", 0, -1)

	if got := metricValue(t, reg, "pico_http_requests_total", map[string]string{"user": "carol", "status": "200", "method": "GET"}); got != 1 {
		t.Errorf("200 requests = %v, want 1", got)
	}
	if got := metricValue(t, reg, "pico_http_requests_total", map[string]string{"user": "carol", "status": "304", "method": "OTHER"}); got != 1 {
		t.Errorf("304 requests = %v, want 1", got)
	}
	if got := metricValue(t, reg, "pico_http_response_bytes_total", map[string]string{"user": "carol"}); got != 7 {
		t.Errorf("bytes = %v, want 7", got)
	}
	if got := metricValue(t, reg, "pico_http_requests_total", map[string]string{"user": "carol", "resource": "app"}); got != 1 {
		t.Errorf("carol/app requests = %v, want 1", got)
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
