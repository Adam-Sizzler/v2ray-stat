package util

import (
	"testing"
)

func BenchmarkValidateUUIDsAllowEmpty(b *testing.B) {
	uuids := []string{
		"a8e02c5f-1c3b-4678-9011-fe0c30d9920d",
		"16cde3a2-54cb-4006-9f13-0a6dba0335a0",
		"d4e0820e-ed95-9367-4449-382ee6355ca9",
		"2a379bd7-aeaa-4afd-b469-af84a8d3b97c",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ValidateUUIDsAllowEmpty(uuids)
	}
}
