package scopecatalog

// EndpointScope represents a single API endpoint permission descriptor.
type EndpointScope struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
}

// ResourceScopes represents a resource and all its grantable scopes and endpoints.
type ResourceScopes struct {
	Resource       string          `json:"resource"`
	ResourceScopes []string        `json:"resourceScopes"`
	Endpoints      []EndpointScope `json:"endpoints"`
}

// GroupedCatalog represents the top-level structure returned by GET /api/tokens/scopes.
type GroupedCatalog struct {
	Wildcard  string           `json:"wildcard"`
	Resources []ResourceScopes `json:"resources"`
}

// EndpointDef is the static definition of an endpoint in the registry.
type EndpointDef struct {
	Resource    string
	Slug        string
	Kind        string
	Method      string
	Path        string
	Description string
}
