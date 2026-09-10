package scopecatalog

import (
	"fmt"
	"sort"
	"sync"

	"exodus/internal/config"
	"exodus/internal/logger"
)

const (
	ScopeWildcard   = "*"
	ScopeActionRead = "read"
	ScopeActionWrite = "write"
)

func BuildResourceScope(resource string) string {
	return resource + ":*"
}

func BuildActionScope(resource, action string) string {
	return resource + ":" + action
}

func BuildEndpointScope(resource, slug string) string {
	return resource + ":" + slug
}

// Registry manages the API token scope catalog dynamically in memory.
type Registry struct {
	mu            sync.RWMutex
	endpoints     []EndpointScope
	validScopes   map[string]struct{}
	byResource    map[string]*ResourceScopes
	resourceOrder []string
	cachedCatalog *GroupedCatalog
}

var (
	defaultRegistryOnce sync.Once
	defaultRegistry     *Registry
)

func DefaultRegistry() *Registry {
	defaultRegistryOnce.Do(func() {
		defaultRegistry = NewRegistry()
		for _, res := range CanonicalResourceOrder {
			defaultRegistry.RegisterResource(res)
		}
		for _, ep := range StaticEndpoints {
			if err := defaultRegistry.RegisterEndpoint(ep); err != nil {
				panic(fmt.Sprintf("failed to register endpoint scope: %v", err))
			}
		}
	})
	return defaultRegistry
}

func NewRegistry() *Registry {
	reg := &Registry{
		endpoints:     make([]EndpointScope, 0, len(StaticEndpoints)),
		validScopes:   make(map[string]struct{}),
		byResource:    make(map[string]*ResourceScopes),
		resourceOrder: make([]string, 0, len(CanonicalResourceOrder)),
	}
	reg.validScopes[ScopeWildcard] = struct{}{}
	return reg
}

func (r *Registry) RegisterResource(resource string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.registerResourceLocked(resource)
}

func (r *Registry) registerResourceLocked(resource string) *ResourceScopes {
	if grp, ok := r.byResource[resource]; ok {
		return grp
	}

	r.resourceOrder = append(r.resourceOrder, resource)
	r.validScopes[BuildResourceScope(resource)] = struct{}{}
	r.validScopes[BuildActionScope(resource, ScopeActionRead)] = struct{}{}
	r.validScopes[BuildActionScope(resource, ScopeActionWrite)] = struct{}{}

	grp := &ResourceScopes{
		Resource: resource,
		ResourceScopes: []string{
			BuildResourceScope(resource),
			BuildActionScope(resource, ScopeActionRead),
			BuildActionScope(resource, ScopeActionWrite),
		},
		Endpoints: make([]EndpointScope, 0),
	}
	r.byResource[resource] = grp
	r.cachedCatalog = nil
	return grp
}

func (r *Registry) RegisterEndpoint(def EndpointDef) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	grp, ok := r.byResource[def.Resource]
	if !ok {
		grp = r.registerResourceLocked(def.Resource)
	}

	key := BuildEndpointScope(def.Resource, def.Slug)
	if _, exists := r.validScopes[key]; exists {
		return fmt.Errorf("duplicate API token scope %q — endpoint scope slugs must be unique within a resource", key)
	}

	ep := EndpointScope{
		Key:         key,
		Kind:        def.Kind,
		Method:      def.Method,
		Path:        def.Path,
		Description: def.Description,
	}

	r.validScopes[key] = struct{}{}
	r.endpoints = append(r.endpoints, ep)
	grp.Endpoints = append(grp.Endpoints, ep)
	r.cachedCatalog = nil
	return nil
}

func (r *Registry) GetGroupedCatalog() GroupedCatalog {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cachedCatalog != nil {
		return *r.cachedCatalog
	}

	orderMap := make(map[string]int, len(r.resourceOrder))
	for idx, name := range r.resourceOrder {
		orderMap[name] = idx
	}

	resources := make([]ResourceScopes, 0, len(r.byResource))
	for _, grp := range r.byResource {
		cp := ResourceScopes{
			Resource:       grp.Resource,
			ResourceScopes: append([]string(nil), grp.ResourceScopes...),
			Endpoints:      append([]EndpointScope(nil), grp.Endpoints...),
		}
		resources = append(resources, cp)
	}

	sort.SliceStable(resources, func(i, j int) bool {
		idxI, okI := orderMap[resources[i].Resource]
		if !okI {
			idxI = 999999
		}
		idxJ, okJ := orderMap[resources[j].Resource]
		if !okJ {
			idxJ = 999999
		}
		return idxI < idxJ
	})

	cat := &GroupedCatalog{
		Wildcard:  ScopeWildcard,
		Resources: resources,
	}
	r.cachedCatalog = cat
	return *cat
}

func (r *Registry) GetResources() []ResourceScopes {
	return r.GetGroupedCatalog().Resources
}

func (r *Registry) GetEndpoints() []EndpointScope {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]EndpointScope, len(r.endpoints))
	copy(out, r.endpoints)
	return out
}

func (r *Registry) GetValidScopes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.validScopes))
	for s := range r.validScopes {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func (r *Registry) FindInvalidScopes(scopes []string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var invalid []string
	for _, scope := range scopes {
		if _, ok := r.validScopes[scope]; !ok {
			invalid = append(invalid, scope)
		}
	}
	return invalid
}

func (r *Registry) Counts() (endpointCount int, scopeCount int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.endpoints), len(r.validScopes)
}

// Package-level helpers delegating to defaultRegistry

func GetGroupedCatalog() GroupedCatalog {
	return DefaultRegistry().GetGroupedCatalog()
}

func GetResources() []ResourceScopes {
	return DefaultRegistry().GetResources()
}

func GetEndpoints() []EndpointScope {
	return DefaultRegistry().GetEndpoints()
}

func GetValidScopes() []string {
	return DefaultRegistry().GetValidScopes()
}

func FindInvalidScopes(scopes []string) []string {
	return DefaultRegistry().FindInvalidScopes(scopes)
}

func LogScopeCatalog(cfg *config.BackendConfig) {
	if cfg == nil || cfg.Logger == nil {
		return
	}
	epCount, scopeCount := DefaultRegistry().Counts()
	cfg.Logger.RoleService(logger.RoleAPI, "ScopeCatalog").Info(
		fmt.Sprintf("Scope catalog built: %d endpoints, %d grantable scopes", epCount, scopeCount),
	)
}
