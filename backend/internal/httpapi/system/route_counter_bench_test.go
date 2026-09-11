package system

import (
	"testing"
)

func BenchmarkRouteCounterIncrement(b *testing.B) {
	rc := NewRouteCounter(nil, nil)
	// Pre-register routes
	routes := []string{
		"GET /api/users",
		"GET /api/nodes",
		"POST /api/nodes",
		"GET /api/system",
		"POST /api/auth/login",
	}
	for _, r := range routes {
		rc.Register(r)
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			rc.Increment(routes[i%len(routes)])
			i++
		}
	})
}
