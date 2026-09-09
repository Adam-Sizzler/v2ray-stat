package panelsettings

import (
	"testing"
)

func TestScopeCatalogCounts(t *testing.T) {
	resources := buildAPITokenScopes(nil)
	if len(resources) != 21 {
		t.Fatalf("expected 21 resources, got %d", len(resources))
	}

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

	if endpointsCount != 196 {
		t.Fatalf("expected 196 endpoints, got %d", endpointsCount)
	}

	if len(scopeSet) != 260 {
		t.Fatalf("expected 260 grantable scopes, got %d", len(scopeSet))
	}
}
