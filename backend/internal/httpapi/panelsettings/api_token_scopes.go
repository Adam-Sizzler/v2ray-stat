package panelsettings

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"exodus/internal/config"
)

//go:embed full_scope_catalog.json
var embeddedScopeCatalogJSON []byte

type apiTokenEndpointScope struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
}

type apiTokenResourceScopes struct {
	Resource       string                  `json:"resource"`
	ResourceScopes []string                `json:"resourceScopes"`
	Endpoints      []apiTokenEndpointScope `json:"endpoints"`
}

var (
	cachedResourcesOnce sync.Once
	cachedResources     []apiTokenResourceScopes
)

func buildAPITokenScopes(_ *config.BackendConfig) []apiTokenResourceScopes {
	cachedResourcesOnce.Do(func() {
		var doc struct {
			Wildcard  string                   `json:"wildcard"`
			Resources []apiTokenResourceScopes `json:"resources"`
		}
		if err := json.Unmarshal(embeddedScopeCatalogJSON, &doc); err == nil && len(doc.Resources) > 0 {
			cachedResources = doc.Resources
		}
	})
	return cachedResources
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
	if cfg == nil || cfg.Logger == nil {
		return
	}
	resources := buildAPITokenScopes(cfg)
	endpointsCount := 0
	scopeSet := make(map[string]struct{})
	scopeSet["*"] = struct{}{}
	for _, res := range resources {
		for _, s := range res.ResourceScopes {
			scopeSet[s] = struct{}{}
		}
		for _, ep := range res.Endpoints {
			endpointsCount++
			if ep.Key != "" {
				scopeSet[ep.Key] = struct{}{}
			}
		}
	}
	cfg.Logger.RoleService("API", "ScopeCatalog").Info(
		fmt.Sprintf("Scope catalog built: %d endpoints, %d grantable scopes", endpointsCount, len(scopeSet)),
	)
}
