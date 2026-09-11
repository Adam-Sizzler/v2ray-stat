package subscription

import (
	"net/http"
	"testing"
)

func BenchmarkIsDomainAddress(b *testing.B) {
	domains := []string{
		"s-backup.online",
		"192.168.1.1",
		"2001:db8::1",
		"[2001:db8::1]",
		"sub.domain.example.com",
		"10.0.0.1",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = isDomainAddress(domains[i%len(domains)])
	}
}

func BenchmarkInferClientAppFromUserAgent(b *testing.B) {
	uas := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64)",
		"v2rayNG/1.8.12 (Android 14; Pixel 8)",
		"sing-box/1.14.0 (Windows)",
		"ClashMeta/2.0.0",
		"Shadowrocket/2.2.34 (iOS 17.5)",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = inferClientAppFromUserAgent(uas[i%len(uas)])
	}
}

func BenchmarkExtractSyntheticHwidHeaders(b *testing.B) {
	req, _ := http.NewRequest("GET", "/sub/abc", nil)
	req.Header.Set("User-Agent", "v2rayNG/1.8.12 (Android 14; Pixel 8)")
	req.Header.Set("X-Device-OS", "Android")
	req.Header.Set("X-Ver-OS", "14")
	req.Header.Set("X-Device-Model", "Pixel 8")

	userUUID := "a8e02c5f-1c3b-4678-9011-fe0c30d9920d"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = extractSyntheticHwidHeaders(req, userUUID, "1.2.3.4")
	}
}
