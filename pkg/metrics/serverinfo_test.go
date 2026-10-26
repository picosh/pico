package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestRegisterServerInfo(t *testing.T) {
	t.Setenv("NODENAME", "ash")
	reg := prometheus.NewRegistry()

	RegisterServerInfoCommit(reg, "pgs", "abc123")

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(families) != 1 || families[0].GetName() != "pico_server_info" {
		t.Fatalf("got %d families, want only pico_server_info", len(families))
	}
	m := families[0].GetMetric()[0]
	labels := map[string]string{}
	for _, l := range m.GetLabel() {
		labels[l.GetName()] = l.GetValue()
	}
	if labels["app"] != "pgs" || labels["nodename"] != "ash" || labels["commit"] != "abc123" {
		t.Errorf("labels = %v, want app=pgs nodename=ash commit=abc123", labels)
	}
	if got := m.GetGauge().GetValue(); got != 1 {
		t.Errorf("value = %v, want 1", got)
	}
}
