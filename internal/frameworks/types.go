package frameworks

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type PathParam struct {
	Name string `json:"name"` // e.g. user_id
	Type string `json:"type"` // e.g. int, string
}

type Endpoint struct {
	Method      string      `json:"method"`       // e.g. GET, POST
	Path        string      `json:"path"`         // e.g. /api/v1/users/<int:user_id>
	HandlerFunc string      `json:"handler_func"` // e.g. get_user
	File        string      `json:"file"`         // e.g. routes.py
	Line        int         `json:"line"`         // start line of decorator
	Framework   string      `json:"framework"`    // e.g. flask
	PathParams  []PathParam `json:"path_params"`  // list of extracted parameters
}

// LoadDynamicEndpoints reads the routes a framework hook recorded at runtime from
// `<root>/.cadr/cache/endpoints.json`. A missing file is returned as an error so
// callers can treat it as "no routes known" rather than a failure.
func LoadDynamicEndpoints(root string) ([]Endpoint, error) {
	data, err := os.ReadFile(filepath.Join(root, ".cadr", "cache", "endpoints.json"))
	if err != nil {
		return nil, err
	}
	var endpoints []Endpoint
	if err := json.Unmarshal(data, &endpoints); err != nil {
		return nil, err
	}
	return endpoints, nil
}
