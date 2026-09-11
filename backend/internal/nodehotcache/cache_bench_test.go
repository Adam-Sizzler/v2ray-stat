package nodehotcache

import (
	"encoding/json"
	"testing"
)

func BenchmarkValidJSON(b *testing.B) {
	raw := `{"cpus":4,"memoryTotal":8589934592,"cpuModel":"AMD Ryzen 7 5800H","os":"linux"}`

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = validJSON(raw)
	}
}

func BenchmarkMgetString(b *testing.B) {
	val := []byte(`{"cpu":15.5,"memoryUsed":2147483648}`)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = mgetString(val)
	}
}

func BenchmarkHotCacheGetManyProcessing(b *testing.B) {
	infoRaw := `{"cpus":4,"memoryTotal":8589934592}`
	statsRaw := `{"cpu":15.5,"memoryUsed":2147483648}`
	versionsRaw := `{"singbox":"1.13.3","node":"26.9.9"}`
	onlineRaw := "42"
	uptimeRaw := "3600"

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		hot := HotCache{
			SingboxUptime: parseInt64(uptimeRaw),
			UsersOnline:   int(parseInt64(onlineRaw)),
		}
		if validJSON(infoRaw) && validJSON(statsRaw) {
			hot.System = &NodeSystem{
				Info:  json.RawMessage(infoRaw),
				Stats: json.RawMessage(statsRaw),
			}
		}
		if versionsRaw != "" {
			var versions NodeVersions
			_ = json.Unmarshal([]byte(versionsRaw), &versions)
			hot.Versions = &versions
		}
	}
}

func BenchmarkHotCacheGetManyBytesProcessing(b *testing.B) {
	infoRaw := []byte(`{"cpus":4,"memoryTotal":8589934592}`)
	statsRaw := []byte(`{"cpu":15.5,"memoryUsed":2147483648}`)
	versionsRaw := []byte(`{"singbox":"1.13.3","node":"26.9.9"}`)
	onlineRaw := "42"
	uptimeRaw := "3600"

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		hot := HotCache{
			SingboxUptime: parseInt64(uptimeRaw),
			UsersOnline:   int(parseInt64(onlineRaw)),
		}
		if len(infoRaw) > 0 && len(statsRaw) > 0 {
			hot.System = &NodeSystem{
				Info:  infoRaw,
				Stats: statsRaw,
			}
		}
		if len(versionsRaw) > 0 {
			var versions NodeVersions
			_ = json.Unmarshal(versionsRaw, &versions)
			hot.Versions = &versions
		}
	}
}
