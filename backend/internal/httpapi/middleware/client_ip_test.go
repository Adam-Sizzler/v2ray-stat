package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetClientIPUsesForwardedForPublicAddressBeforeProxyRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.18.0.8:47242"
	req.Header.Set("X-Forwarded-For", "144.31.119.150, 172.18.0.1")
	req.Header.Set("X-Real-IP", "172.18.0.1")

	if got := GetClientIP(req, nil); got != "144.31.119.150" {
		t.Fatalf("GetClientIP got %q, want %q", got, "144.31.119.150")
	}
}

func TestGetClientIPUsesExodusRealIPHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.18.0.8:47242"
	req.Header.Set(ExodusRealIPHeader, "144.31.119.150")
	req.Header.Set("X-Forwarded-For", "172.18.0.1")

	if got := GetClientIP(req, nil); got != "144.31.119.150" {
		t.Fatalf("GetClientIP got %q, want %q", got, "144.31.119.150")
	}
}

func TestWithClientIPStoresResolvedIPInContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.18.0.8:47242"
	req.Header.Set("X-Forwarded-For", "144.31.119.150, 172.18.0.1")

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := GetClientIP(r, nil); got != "144.31.119.150" {
			t.Fatalf("GetClientIP from context got %q, want %q", got, "144.31.119.150")
		}
	})

	WithClientIP(nil, next).ServeHTTP(httptest.NewRecorder(), req)
}

func BenchmarkResolveClientIP(b *testing.B) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.18.0.8:47242"
	req.Header.Set("X-Forwarded-For", "144.31.119.150, 172.18.0.1")
	req.Header.Set("X-Real-IP", "172.18.0.1")

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = ResolveClientIP(req, nil)
	}
}
