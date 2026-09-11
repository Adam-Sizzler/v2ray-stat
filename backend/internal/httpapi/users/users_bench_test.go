package users

import (
	"testing"
)

func BenchmarkGenerateRandomString(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = generateRandomString(16)
	}
}

func BenchmarkGenerateCustomShortUUID(b *testing.B) {
	pattern := "user-####-****-aaaa"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = generateCustomShortUUID(pattern)
	}
}
