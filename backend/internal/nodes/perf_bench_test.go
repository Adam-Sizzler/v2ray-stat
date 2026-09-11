package users

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"exodus/internal/proto"
)

func BenchmarkBulkUpsertQueryBuilder(b *testing.B) {
	type testDelta struct {
		UserID       int64
		TotalBytes   int64
		HistoryBytes int64
	}
	deltas := make([]testDelta, 1000)
	for i := range deltas {
		deltas[i] = testDelta{
			UserID:       int64(i + 1),
			TotalBytes:   int64((i + 1) * 1024),
			HistoryBytes: int64((i + 1) * 1024),
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		var query strings.Builder
		query.Grow(len(deltas)*48 + 512)
		query.WriteString(`
			INSERT INTO user_traffic (
				id, used_traffic_bytes, lifetime_used_traffic_bytes,
				online_at, last_connected_node_uuid, first_connected_at
			)
			SELECT
				v.id,
				v.total_bytes,
				v.total_bytes,
				now(),
				v.last_connected_node_uuid,
				now()
			FROM (VALUES `)

		idx := 1
		for j := range deltas {
			if j > 0 {
				query.WriteString(", ")
			}
			writePlaceholder3(&query, idx, "uuid")
			idx += 3
		}
		query.WriteString(`) AS v(id, total_bytes, last_connected_node_uuid)`)
		_ = query.String()
	}
}

func BenchmarkExtractTrafficStatsDelta(b *testing.B) {
	stats := make([]*proto.Stat, 0, 10000)
	stats = append(stats,
		&proto.Stat{Name: "core_status", Value: "running"},
		&proto.Stat{Name: "singbox_version", Value: "1.13.3"},
		&proto.Stat{Name: "node_version", Value: "26.9.9"},
		&proto.Stat{Name: "singbox_uptime", Value: "3600"},
		&proto.Stat{Name: "system_info", Value: `{"cpus":4,"memoryTotal":8589934592}`},
		&proto.Stat{Name: "system_stats", Value: `{"cpu":15.5,"memoryUsed":2147483648}`},
	)
	for i := 1; i <= 4997; i++ {
		uid := strconv.Itoa(i)
		stats = append(stats,
			&proto.Stat{Name: "user>>>" + uid + ">>>traffic>>>uplink", Value: "1500"},
			&proto.Stat{Name: "user>>>" + uid + ">>>traffic>>>downlink", Value: "4500"},
		)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = extractTrafficStatsDelta(stats)
	}
}

func BenchmarkStatsValuesMapAllocation(b *testing.B) {
	stats := make([]*proto.Stat, 0, 10000)
	stats = append(stats,
		&proto.Stat{Name: "core_status", Value: "running"},
		&proto.Stat{Name: "core_error", Value: ""},
		&proto.Stat{Name: "singbox_version", Value: "1.13.3"},
		&proto.Stat{Name: "node_version", Value: "26.9.9"},
		&proto.Stat{Name: "singbox_uptime", Value: "3600"},
		&proto.Stat{Name: "system_info", Value: `{"cpus":4,"memoryTotal":8589934592}`},
		&proto.Stat{Name: "system_stats", Value: `{"cpu":15.5,"memoryUsed":2147483648}`},
	)
	for i := 1; i <= 4996; i++ {
		uid := strconv.Itoa(i)
		stats = append(stats,
			&proto.Stat{Name: "user>>>" + uid + ">>>traffic>>>uplink", Value: "1500"},
			&proto.Stat{Name: "user>>>" + uid + ">>>traffic>>>downlink", Value: "4500"},
		)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		var (
			rawCoreStatus     string
			rawCoreError      string
			rawSingboxVersion string
			rawNodeVersion    string
			rawSingboxUptime  string
			rawSystemInfo     string
			rawSystemStats    string
		)
		for _, stat := range stats {
			if stat == nil {
				continue
			}
			name := stat.GetName()
			if len(name) > 6 && (name[0] == 'u' || name[0] == 'i' || name[0] == 'o') && strings.Contains(name, ">>>") {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "core_status":
				rawCoreStatus = stat.GetValue()
			case "core_error":
				rawCoreError = stat.GetValue()
			case "singbox_version":
				rawSingboxVersion = stat.GetValue()
			case "node_version":
				rawNodeVersion = stat.GetValue()
			case "singbox_uptime":
				rawSingboxUptime = stat.GetValue()
			case "system_info":
				rawSystemInfo = stat.GetValue()
			case "system_stats":
				rawSystemStats = stat.GetValue()
			}
		}
		_ = strings.ToLower(strings.TrimSpace(rawCoreStatus))
		_ = strings.TrimSpace(rawCoreError)
		_ = rawSingboxVersion
		_ = rawNodeVersion
		_ = rawSingboxUptime
		_ = rawSystemInfo
		_ = rawSystemStats
	}
}

func BenchmarkBuildInboundUsers(b *testing.B) {
	users := make([]inboundUserCredentials, 5000)
	for i := range users {
		users[i] = inboundUserCredentials{
			ID:             int64(i + 1),
			Username:       "user_" + strconv.Itoa(i+1),
			VLESSUUID:      "a0000000-0000-0000-0000-" + fmt.Sprintf("%012d", i+1),
			TrojanPassword: "pwd_" + strconv.Itoa(i+1),
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = buildInboundUsers("vless", users)
	}
}
