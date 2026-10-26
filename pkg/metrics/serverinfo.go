// Package metrics holds the Prometheus metrics every pico process exports,
// including the SSH servers, so it can't depend on pkg/shared.
package metrics

import (
	"os"
	"runtime/debug"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// RegisterServerInfo exports pico_server_info for app. Its value is always 1,
// so a query can join it on instance and app to name the server and commit
// behind any other series.
func RegisterServerInfo(reg prometheus.Registerer, app string) {
	RegisterServerInfoCommit(reg, app, commit())
}

// RegisterServerInfoCommit is RegisterServerInfo for a binary built without
// VCS stamping, which sets its commit through -ldflags instead.
func RegisterServerInfoCommit(reg prometheus.Registerer, app, commit string) {
	promauto.With(reg).NewGauge(prometheus.GaugeOpts{
		Namespace: "pico",
		Name:      "server_info",
		Help:      "The server and commit a process runs on. Always 1.",
		ConstLabels: prometheus.Labels{
			"app":      app,
			"nodename": os.Getenv("NODENAME"),
			"commit":   commit,
		},
	}).Set(1)
}

// commit is the VCS revision Go stamped into the binary, with -dirty when the
// tree had uncommitted changes.
func commit() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	var revision string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision != "" && modified {
		revision += "-dirty"
	}
	return revision
}
