package server

import (
	"html"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

func renderIndexTemplate(templateContent, metaTitle, metaDescription, panelData string) string {
	escapedDesc := html.EscapeString(metaDescription)
	escapedTitle := html.EscapeString(metaTitle)
	replacer := strings.NewReplacer(
		"<%- panelData %>", panelData,
		"<%= metaDescription %>", escapedDesc,
		"<%- metaDescription %>", escapedDesc,
		"<%= metaTitle %>", escapedTitle,
		"<%- metaTitle %>", escapedTitle,
	)
	return replacer.Replace(templateContent)
}

func getRealIP(r *http.Request, trustProxySetting string) string {
	trustProxySetting = strings.TrimSpace(trustProxySetting)
	if trustProxySetting == "" {
		trustProxySetting = "1"
	}

	if strings.EqualFold(trustProxySetting, "false") || strings.EqualFold(trustProxySetting, "none") || trustProxySetting == "0" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil && host != "" {
			return host
		}
		if r.RemoteAddr != "" {
			return r.RemoteAddr
		}
		return "0.0.0.0"
	}

	forwardedFor := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if forwardedFor != "" {
		parts := strings.Split(forwardedFor, ",")
		cleanedParts := make([]string, 0, len(parts))
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				cleanedParts = append(cleanedParts, trimmed)
			}
		}

		if len(cleanedParts) > 0 {
			if strings.EqualFold(trustProxySetting, "true") || strings.EqualFold(trustProxySetting, "all") {
				return cleanedParts[0]
			}

			if numHops, err := strconv.Atoi(trustProxySetting); err == nil && numHops > 0 {
				if len(cleanedParts) >= numHops {
					return cleanedParts[len(cleanedParts)-numHops]
				}
				return cleanedParts[0]
			}

			matchers := parseTrustProxyMatchers(trustProxySetting)
			clientIP := cleanedParts[0]
			for i := len(cleanedParts) - 1; i >= 0; i-- {
				curr := cleanedParts[i]
				if !isTrustedIP(curr, matchers) {
					clientIP = curr
					break
				}
			}
			return clientIP
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}

	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}

	return "0.0.0.0"
}

type trustMatcher interface {
	match(ip net.IP) bool
}

type cidrMatcher struct {
	network *net.IPNet
}

func (c cidrMatcher) match(ip net.IP) bool {
	return c.network.Contains(ip)
}

type exactIPMatcher struct {
	ip net.IP
}

func (e exactIPMatcher) match(ip net.IP) bool {
	return e.ip.Equal(ip)
}

func parseTrustProxyMatchers(raw string) []trustMatcher {
	parts := strings.Split(raw, ",")
	matchers := make([]trustMatcher, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.ToLower(strings.TrimSpace(p))
		if trimmed == "" {
			continue
		}
		switch trimmed {
		case "loopback":
			_, n1, _ := net.ParseCIDR("127.0.0.0/8")
			_, n2, _ := net.ParseCIDR("::1/128")
			if n1 != nil {
				matchers = append(matchers, cidrMatcher{network: n1})
			}
			if n2 != nil {
				matchers = append(matchers, cidrMatcher{network: n2})
			}
		case "linklocal":
			_, n1, _ := net.ParseCIDR("169.254.0.0/16")
			_, n2, _ := net.ParseCIDR("fe80::/10")
			if n1 != nil {
				matchers = append(matchers, cidrMatcher{network: n1})
			}
			if n2 != nil {
				matchers = append(matchers, cidrMatcher{network: n2})
			}
		case "uniquelocal":
			_, n1, _ := net.ParseCIDR("10.0.0.0/8")
			_, n2, _ := net.ParseCIDR("172.16.0.0/12")
			_, n3, _ := net.ParseCIDR("192.168.0.0/16")
			_, n4, _ := net.ParseCIDR("fc00::/7")
			if n1 != nil {
				matchers = append(matchers, cidrMatcher{network: n1})
			}
			if n2 != nil {
				matchers = append(matchers, cidrMatcher{network: n2})
			}
			if n3 != nil {
				matchers = append(matchers, cidrMatcher{network: n3})
			}
			if n4 != nil {
				matchers = append(matchers, cidrMatcher{network: n4})
			}
		default:
			if strings.Contains(trimmed, "/") {
				_, n, err := net.ParseCIDR(trimmed)
				if err == nil && n != nil {
					matchers = append(matchers, cidrMatcher{network: n})
				}
			} else {
				ip := net.ParseIP(trimmed)
				if ip != nil {
					matchers = append(matchers, exactIPMatcher{ip: ip})
				}
			}
		}
	}
	return matchers
}

func isTrustedIP(ipStr string, matchers []trustMatcher) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	for _, m := range matchers {
		if m.match(ip) {
			return true
		}
	}
	return false
}

func cleanPath(rawPath string) string {
	if rawPath == "" {
		return "/"
	}

	cleaned := filepath.ToSlash(filepath.Clean("/" + rawPath))
	if cleaned == "." {
		return "/"
	}

	return cleaned
}

func splitSegments(requestPath string) []string {
	if requestPath == "/" || requestPath == "" {
		return nil
	}

	parts := strings.Split(strings.Trim(requestPath, "/"), "/")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			result = append(result, part)
		}
	}

	return result
}

func containsDotfile(requestPath string) bool {
	for _, segment := range splitSegments(requestPath) {
		if strings.HasPrefix(segment, ".") {
			return true
		}
	}

	return false
}

func closeConnection(w http.ResponseWriter) {
	if hijacker, ok := w.(http.Hijacker); ok {
		conn, _, err := hijacker.Hijack()
		if err == nil {
			_ = conn.Close()
			return
		}
	}
	panic(http.ErrAbortHandler)
}

func prefixAssetsInHTML(content, prefix string) string {
	trimmed := strings.Trim(prefix, "/")
	if trimmed == "" {
		return content
	}

	assetPrefix := "/" + trimmed + "/"
	replacer := strings.NewReplacer(
		`"/assets/`, `"`+assetPrefix+`assets/`,
		`'/assets/`, `'`+assetPrefix+`assets/`,
		`(/assets/`, `(`+assetPrefix+`assets/`,
		`"/locales/`, `"`+assetPrefix+`locales/`,
		`'/locales/`, `'`+assetPrefix+`locales/`,
		`(/locales/`, `(`+assetPrefix+`locales/`,
	)

	return replacer.Replace(content)
}
