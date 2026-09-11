package system

import (
	"testing"
)

func TestParsePrometheusLine(t *testing.T) {
	line := `exodus_node_online_users{node_uuid="node-123",country_code="US"} 42`
	sample, ok := parsePrometheusLine(line)
	if !ok {
		t.Fatalf("expected parsePrometheusLine to succeed")
	}
	if sample.Name != "exodus_node_online_users" {
		t.Errorf("got name %q, want %q", sample.Name, "exodus_node_online_users")
	}
	if sample.Value != 42 {
		t.Errorf("got value %v, want 42", sample.Value)
	}
	if sample.Labels["node_uuid"] != "node-123" {
		t.Errorf("got node_uuid %q, want node-123", sample.Labels["node_uuid"])
	}
	if sample.Labels["country_code"] != "US" {
		t.Errorf("got country_code %q, want US", sample.Labels["country_code"])
	}
}

func BenchmarkParsePrometheusLine(b *testing.B) {
	line := `exodus_node_online_users{node_uuid="node-123",country_code="US"} 42`
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = parsePrometheusLine(line)
	}
}
