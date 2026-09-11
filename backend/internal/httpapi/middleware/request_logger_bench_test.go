package middleware

import (
	"testing"
)

func BenchmarkFormatRequestLogMessage(b *testing.B) {
	var buf [128]byte
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = formatRequestLogMessage(buf[:0], "POST", "/api/users/stream", 200, 42)
	}
}
