package panelsettings

import (
	"encoding/json"
	"fmt"
	"strings"

	"exodus/internal/config"
	"exodus/internal/httpapi/scopecatalog"
)

type apiTokenEndpointScope = scopecatalog.EndpointScope
type apiTokenResourceScopes = scopecatalog.ResourceScopes

func buildAPITokenScopes(_ *config.BackendConfig) []apiTokenResourceScopes {
	return scopecatalog.GetResources()
}

func normalizeAPITokenScopes(scopes []string) []string {
	out := make([]string, 0, len(scopes))
	seen := map[string]struct{}{}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if strings.HasPrefix(scope, "ip-control:") {
			scope = "connections:" + strings.TrimPrefix(scope, "ip-control:")
		} else if strings.HasPrefix(scope, "ip_control:") {
			scope = "connections:" + strings.TrimPrefix(scope, "ip_control:")
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	if len(out) == 0 {
		return []string{"*"}
	}
	return out
}

func parseAPITokenScopes(raw string) []string {
	var scopes []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &scopes); err != nil {
		return []string{"*"}
	}
	return normalizeAPITokenScopes(scopes)
}

func postgresTextArrayLiteral(items []string) string {
	items = normalizeAPITokenScopes(items)
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.ReplaceAll(item, `\`, `\\`)
		item = strings.ReplaceAll(item, `"`, `\"`)
		quoted = append(quoted, fmt.Sprintf(`"%s"`, item))
	}
	return "{" + strings.Join(quoted, ",") + "}"
}

func LogScopeCatalog(cfg *config.BackendConfig) {
	scopecatalog.LogScopeCatalog(cfg)
}
