package subscriptionnodes

import (
	"strconv"
	"testing"

	"exodus/internal/proto"
)

func BenchmarkUpdateRuntimeFromStats(b *testing.B) {
	sm := &SubNodeMonitor{
		runtimeByNodeName: make(map[string]SubNodeRuntimeSnapshot),
	}

	stats := []*proto.Stat{
		{Name: "sub_node_version", Value: "1.2.3"},
		{Name: "singbox_version", Value: "1.14.0"},
		{Name: "sub_node_uptime", Value: "123456"},
		{Name: "cpu_count", Value: "8"},
		{Name: "cpu_model", Value: "AMD EPYC"},
		{Name: "total_ram", Value: "16384"},
		{Name: "irrelevant_stat_1", Value: "value_1"},
		{Name: "irrelevant_stat_2", Value: "value_2"},
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nodeName := "node-" + strconv.Itoa(i%10)
		sm.updateRuntimeFromStats(nodeName, stats)
	}
}
