package scopecatalog

import (
	"testing"
)

func TestDynamicScopeCatalog(t *testing.T) {
	cat := GetGroupedCatalog()
	if cat.Wildcard != "*" {
		t.Fatalf("expected wildcard '*', got %s", cat.Wildcard)
	}

	if len(cat.Resources) != 21 {
		t.Fatalf("expected 21 resources, got %d", len(cat.Resources))
	}

	endpointsCount := 0
	scopeSet := make(map[string]struct{})
	scopeSet["*"] = struct{}{}

	for idx, res := range cat.Resources {
		// Check canonical resource ordering
		if idx < len(CanonicalResourceOrder) && res.Resource != CanonicalResourceOrder[idx] {
			t.Errorf("resource at idx %d mismatch: expected %s, got %s", idx, CanonicalResourceOrder[idx], res.Resource)
		}

		// Check resourceScopes: [resource:*, resource:read, resource:write]
		expectedScopes := []string{
			res.Resource + ":*",
			res.Resource + ":read",
			res.Resource + ":write",
		}
		if len(res.ResourceScopes) != len(expectedScopes) {
			t.Fatalf("resource %s expected %d resourceScopes, got %d", res.Resource, len(expectedScopes), len(res.ResourceScopes))
		}
		for i, s := range expectedScopes {
			if res.ResourceScopes[i] != s {
				t.Errorf("resource %s scope %d mismatch: expected %s, got %s", res.Resource, i, s, res.ResourceScopes[i])
			}
			scopeSet[s] = struct{}{}
		}

		// Check endpoints
		for _, ep := range res.Endpoints {
			endpointsCount++
			if ep.Key != "" {
				if _, exists := scopeSet[ep.Key]; exists {
					t.Errorf("duplicate scope key detected in endpoints: %s", ep.Key)
				}
				scopeSet[ep.Key] = struct{}{}
			}
			if ep.Method == "" || ep.Path == "" || ep.Description == "" {
				t.Errorf("endpoint %s has empty fields: method=%s, path=%s, desc=%s", ep.Key, ep.Method, ep.Path, ep.Description)
			}
		}
	}

	if endpointsCount != 196 {
		t.Fatalf("expected 196 endpoints, got %d", endpointsCount)
	}

	if len(scopeSet) != 260 {
		t.Fatalf("expected 260 grantable scopes, got %d", len(scopeSet))
	}

	epCount, totalScopes := DefaultRegistry().Counts()
	if epCount != 196 {
		t.Fatalf("DefaultRegistry().Counts() expected 196 endpoints, got %d", epCount)
	}
	if totalScopes != 260 {
		t.Fatalf("DefaultRegistry().Counts() expected 260 scopes, got %d", totalScopes)
	}

	// Test FindInvalidScopes
	invalid := FindInvalidScopes([]string{"users:*", "invalid:scope", "system:exodus-health", "another:bad"})
	if len(invalid) != 2 || invalid[0] != "invalid:scope" || invalid[1] != "another:bad" {
		t.Fatalf("unexpected invalid scopes: %v", invalid)
	}
}

func TestDuplicateEndpointRegistration(t *testing.T) {
	reg := NewRegistry()
	err := reg.RegisterEndpoint(EndpointDef{
		Resource:    "test",
		Slug:        "one",
		Kind:        "read",
		Method:      "GET",
		Path:        "/api/test/one",
		Description: "Test one",
	})
	if err != nil {
		t.Fatalf("first registration failed: %v", err)
	}

	err = reg.RegisterEndpoint(EndpointDef{
		Resource:    "test",
		Slug:        "one",
		Kind:        "read",
		Method:      "GET",
		Path:        "/api/test/one",
		Description: "Test one duplicate",
	})
	if err == nil {
		t.Fatal("expected error on duplicate endpoint slug registration, got nil")
	}
}
