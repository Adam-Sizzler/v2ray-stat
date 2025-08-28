package api

import (
	"fmt"
	"net/http"
	"v2ray-stat/constant"
)

// Answer handles basic server information requests.
func Answer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		serverHeader := fmt.Sprintf("MuxCloud/%s (WebServer)", constant.Version)
		w.Header().Set("Server", serverHeader)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Powered-By", "MuxCloud")
		fmt.Fprintf(w, "MuxCloud / %s\n", constant.Version)
	}
}

// WithServerHeader adds a Server header to all responses.
func WithServerHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverHeader := fmt.Sprintf("MuxCloud/%s (WebServer)", constant.Version)
		w.Header().Set("Server", serverHeader)
		w.Header().Set("X-Powered-By", "MuxCloud")
		next.ServeHTTP(w, r)
	})
}
