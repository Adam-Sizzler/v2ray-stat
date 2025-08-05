package api

import (
	"net"
	"net/http"
	"strings"

	"v2ray-stat/backend/config"
)

// getClientIP retrieves the client IP address from an HTTP request.
func getClientIP(r *http.Request, cfg *config.Config) string {
	cfg.Logger.Debug("Retrieving client IP address", "remote_addr", r.RemoteAddr)

	// Check X-Forwarded-For header first (may contain multiple IPs)
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		ips := strings.Split(fwd, ",")
		ip := strings.TrimSpace(ips[0])
		cfg.Logger.Trace("Using X-Forwarded-For header", "ip", ip)
		return ip
	}

	// Check X-Real-IP header
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		cfg.Logger.Trace("Using X-Real-IP header", "ip", realIP)
		return realIP
	}

	// Fallback to RemoteAddr
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		cfg.Logger.Error("Failed to parse RemoteAddr", "remote_addr", r.RemoteAddr, "error", err)
		return r.RemoteAddr // Return as-is on error
	}
	cfg.Logger.Trace("Using RemoteAddr", "ip", ip)
	return ip
}

// TokenAuthMiddleware verifies the token in the Authorization header.
func TokenAuthMiddleware(cfg *config.Config, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		clientIP := getClientIP(r, cfg)
		cfg.Logger.Debug("Verifying token for request", "client_ip", clientIP)

		// Allow access if no API token is set
		if cfg.API.APIToken == "" {
			cfg.Logger.Warn("API_TOKEN not set, request allowed", "client_ip", clientIP)
			next.ServeHTTP(w, r)
			return
		}

		// Check Authorization header
		authHeader := r.Header.Get("Authorization")
		cfg.Logger.Trace("Read Authorization header", "header", authHeader)
		if authHeader == "" {
			cfg.Logger.Warn("Missing Authorization header", "client_ip", clientIP)
			http.Error(w, "Missing Authorization header", http.StatusUnauthorized)
			return
		}

		// Expect format "Bearer <token>"
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			cfg.Logger.Warn("Invalid Authorization header format", "client_ip", clientIP, "header", authHeader)
			http.Error(w, "Invalid Authorization header format", http.StatusUnauthorized)
			return
		}

		// Verify token
		token := strings.TrimSpace(parts[1])
		if token == "" {
			cfg.Logger.Warn("Empty token in Authorization header", "client_ip", clientIP)
			http.Error(w, "Empty token", http.StatusUnauthorized)
			return
		}
		if token != cfg.API.APIToken {
			cfg.Logger.Warn("Invalid token", "client_ip", clientIP)
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}

		cfg.Logger.Info("Token verified successfully", "client_ip", clientIP)
		next.ServeHTTP(w, r)
	}
}
