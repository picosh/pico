package pgs

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/router"
	"github.com/picosh/pico/pkg/storage"
	"github.com/prometheus/client_golang/prometheus"
)

func TestSiteMetricsCountCacheHits(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	dbpool := NewPgsDb(logger)
	user := dbpool.Users[0]
	st, err := storage.NewStorageMemory(map[string]map[string]string{
		shared.GetAssetBucketName(user.ID): {"/test/index.html": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig(logger, dbpool, newTestStorage(st), discardPubsub{})
	cfg.Domain = "pgs.test"
	httpCache, _ := NewPgsHttpCache(cfg, newWebRouter(cfg))
	reg := prometheus.NewRegistry()
	h := router.NewUserMetrics("pgs", reg, router.UserMetricsOpts{Owner: siteProject(cfg)}).Handler(httpCache)

	var statuses []string
	for range 2 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", dbpool.mkpath("/"), nil))
		statuses = append(statuses, rec.Header().Get("cache-status"))
	}
	if !strings.Contains(statuses[1], "hit") {
		t.Fatalf("second request was not a cache hit: %q", statuses)
	}

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	count := func(name string, labels map[string]string) float64 {
		var total float64
		for _, family := range families {
			if family.GetName() != name {
				continue
			}
		metrics:
			for _, m := range family.GetMetric() {
				matched := 0
				for _, l := range m.GetLabel() {
					if want, ok := labels[l.GetName()]; ok {
						if want != l.GetValue() {
							continue metrics
						}
						matched++
					}
				}
				if matched == len(labels) {
					total += m.GetCounter().GetValue()
				}
			}
		}
		return total
	}
	if got := count("pico_http_requests_total", map[string]string{"user": user.Name}); got != 2 {
		t.Errorf("requests for %s = %v, want 2", user.Name, got)
	}
	if got := count("pico_http_requests_total", map[string]string{"user": user.Name, "resource": "test"}); got != 2 {
		t.Errorf("requests for %s/test = %v, want 2", user.Name, got)
	}
}
