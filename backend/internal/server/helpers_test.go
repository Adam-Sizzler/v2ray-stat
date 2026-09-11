package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/exodus/subscription-page/backend/internal/config"
)

func TestGetRealIP_TrustProxy(t *testing.T) {
	tests := []struct {
		name         string
		trustProxy   string
		remoteAddr   string
		forwardedFor string
		expectedIP   string
	}{
		{
			name:         "Default 1 hop - single proxy",
			trustProxy:   "1",
			remoteAddr:   "172.18.0.2:12345",
			forwardedFor: "203.0.113.195",
			expectedIP:   "203.0.113.195",
		},
		{
			name:         "Default 1 hop - multiple proxies (client, proxy1, proxy2)",
			trustProxy:   "1",
			remoteAddr:   "172.18.0.2:12345",
			forwardedFor: "203.0.113.195, 198.51.100.1, 10.0.0.1",
			expectedIP:   "10.0.0.1",
		},
		{
			name:         "2 hops - multiple proxies",
			trustProxy:   "2",
			remoteAddr:   "172.18.0.2:12345",
			forwardedFor: "203.0.113.195, 198.51.100.1, 10.0.0.1",
			expectedIP:   "198.51.100.1",
		},
		{
			name:         "Trust all (true)",
			trustProxy:   "true",
			remoteAddr:   "172.18.0.2:12345",
			forwardedFor: "203.0.113.195, 198.51.100.1, 10.0.0.1",
			expectedIP:   "203.0.113.195",
		},
		{
			name:         "Trust none (false)",
			trustProxy:   "false",
			remoteAddr:   "198.51.100.5:12345",
			forwardedFor: "203.0.113.195",
			expectedIP:   "198.51.100.5",
		},
		{
			name:         "Preset loopback,uniquelocal",
			trustProxy:   "loopback,uniquelocal",
			remoteAddr:   "127.0.0.1:12345",
			forwardedFor: "203.0.113.195, 10.0.0.5, 127.0.0.1",
			expectedIP:   "203.0.113.195",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr
			if tt.forwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tt.forwardedFor)
			}

			ip := getRealIP(req, tt.trustProxy)
			if ip != tt.expectedIP {
				t.Errorf("expected %s, got %s", tt.expectedIP, ip)
			}
		})
	}
}

func TestApp_ApplyCustomPrefix(t *testing.T) {
	tests := []struct {
		name        string
		subPath     string
		requestPath string
		wantRoute   string
		wantOK      bool
	}{
		{
			name:        "root prefix passes path as is",
			subPath:     "",
			requestPath: "/tbJZ7vCY0JLLx4bH",
			wantRoute:   "/tbJZ7vCY0JLLx4bH",
			wantOK:      true,
		},
		{
			name:        "custom prefix matching request",
			subPath:     "/subscription",
			requestPath: "/subscription/tbJZ7vCY0JLLx4bH",
			wantRoute:   "/tbJZ7vCY0JLLx4bH",
			wantOK:      true,
		},
		{
			name:        "custom prefix with client type",
			subPath:     "/subscription",
			requestPath: "/subscription/tbJZ7vCY0JLLx4bH/clash",
			wantRoute:   "/tbJZ7vCY0JLLx4bH/clash",
			wantOK:      true,
		},
		{
			name:        "custom prefix exact match",
			subPath:     "/subscription",
			requestPath: "/subscription",
			wantRoute:   "/",
			wantOK:      true,
		},
		{
			name:        "root request when custom prefix is configured (no dual-path fallback)",
			subPath:     "/subscription",
			requestPath: "/tbJZ7vCY0JLLx4bH",
			wantRoute:   "",
			wantOK:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := &App{cfg: config.Config{Backend: config.BackendConfig{BasePath: tt.subPath}, SubPath: tt.subPath}}
			route, ok := app.applyCustomPrefix(tt.requestPath)
			if ok != tt.wantOK {
				t.Errorf("applyCustomPrefix() ok = %v, want %v", ok, tt.wantOK)
			}
			if route != tt.wantRoute {
				t.Errorf("applyCustomPrefix() route = %q, want %q", route, tt.wantRoute)
			}
		})
	}
}

func TestRenderIndexTemplate(t *testing.T) {
	template := `<html><head><title><%= metaTitle %></title><meta name="description" content="<%= metaDescription %>"></head><body><script>window.__DATA__ = "<%- panelData %>";</script></body></html>`
	title := "My & VPN"
	desc := "Fast <&> Secure"
	panelData := "base64data123"

	result := renderIndexTemplate(template, title, desc, panelData)

	expectedTitle := "My &amp; VPN"
	expectedDesc := "Fast &lt;&amp;&gt; Secure"
	if !strings.Contains(result, expectedTitle) {
		t.Errorf("expected title %q in result, got: %s", expectedTitle, result)
	}
	if !strings.Contains(result, expectedDesc) {
		t.Errorf("expected desc %q in result, got: %s", expectedDesc, result)
	}
	if !strings.Contains(result, panelData) {
		t.Errorf("expected panel data %q in result, got: %s", panelData, result)
	}
}
