package server

import (
	"net/http"
	"strings"

	"github.com/exodus/subscription-page/backend/internal/proto"
)

// ignoredHeaders mirrors the header block-list, so a client can't spoof
// proxy/CDN/auth/real-ip headers (X-Forwarded-For, CF-Connecting-IP,
// Authorization, etc.) that get forwarded to the panel/node over gRPC.
// Keys are lowercase; matching is case-insensitive.
var ignoredHeaders = map[string]struct{}{
	"accept-encoding":              {},
	"alt-svc":                      {},
	"authorization":                {},
	"cache-control":                {},
	"cf-access-client-id":          {},
	"cf-access-client-secret":      {},
	"cf-cache-status":              {},
	"cf-connecting-ip":             {},
	"cf-ray":                       {},
	"connection":                   {},
	"content-length":               {},
	"content-security-policy":      {},
	"cross-origin-opener-policy":   {},
	"cross-origin-resource-policy": {},
	"expires":                      {},
	"fastly-client-ip":             {},
	"forwarded":                    {},
	"forwarded-for":                {},
	"host":                         {},
	"keep-alive":                   {},
	"nel":                          {},
	"origin-agent-cluster":         {},
	"pragma":                       {},
	"proxy-authenticate":           {},
	"proxy-authorization":          {},
	"report-to":                    {},
	"server":                       {},
	"te":                           {},
	"trailer":                      {},
	"transfer-encoding":            {},
	"true-client-ip":               {},
	"upgrade":                      {},
	"x-api-key":                    {},
	"x-client-ip":                  {},
	"x-cluster-client-ip":          {},
	"x-forwarded":                  {},
	"x-forwarded-for":              {},
	"x-forwarded-proto":            {},
	"x-forwarded-scheme":           {},
	"x-real-ip":                    {},
	"x-exodus-client-type":         {},
	"x-exodus-real-ip":             {},
	"x-subpage-version":            {},
	"etag":                         {},
	"last-modified":                {},
}

// filterForwardHeaders returns a copy of headers with every entry from
// ignoredHeaders removed. Call this on r.Header before it's turned into
// proto headers and forwarded downstream - never forward the raw
// http.Request headers as-is.
func filterForwardHeaders(headers http.Header) http.Header {
	if len(headers) == 0 {
		return headers
	}

	filtered := make(http.Header, len(headers))
	for key, values := range headers {
		if _, ignored := ignoredHeaders[strings.ToLower(key)]; ignored {
			continue
		}
		filtered[key] = values
	}

	return filtered
}

// filterAndConvertToProtoHeaders performs filtering and proto conversion in a
// single pass, eliminating intermediate http.Header map allocations.
func filterAndConvertToProtoHeaders(headers http.Header) []*proto.Header {
	if len(headers) == 0 {
		return nil
	}

	result := make([]*proto.Header, 0, len(headers))
	for key, values := range headers {
		if _, ignored := ignoredHeaders[strings.ToLower(key)]; ignored {
			continue
		}
		for _, value := range values {
			result = append(result, &proto.Header{Key: key, Value: value})
		}
	}
	return result
}
