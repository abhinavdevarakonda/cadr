package frameworks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abhinavdevarakonda/cadr/queries"
)

// loadQuery returns a framework tree-sitter query by file name (e.g. "gin.scm").
// Queries are embedded in the binary, so detection also works when cadr runs
// from a project that is not the cadr repo itself; the on-disk copy is only a
// development fallback.
func loadQuery(name string) ([]byte, error) {
	if data, err := queries.Files.ReadFile(name); err == nil {
		return data, nil
	}
	data, err := os.ReadFile(filepath.Join("queries", name))
	if err != nil {
		return nil, fmt.Errorf("failed to read %s query: %w", name, err)
	}
	return data, nil
}

// projectMentions reports whether any file contains one of the needles. It is
// the cheap project-level gate that runs before a detector builds its
// cross-file constant index.
func projectMentions(files []string, needles ...string) bool {
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		src := string(data)
		for _, needle := range needles {
			if strings.Contains(src, needle) {
				return true
			}
		}
	}
	return false
}

// DetectAll runs every static framework detector over the scanned files and
// returns their endpoints in a stable order (flask, fastapi, gin, node).
// A detector that fails to load its query is skipped rather than hiding the
// endpoints found by the others.
func DetectAll(files []string) []Endpoint {
	var endpoints []Endpoint

	if eps, err := DetectFlaskEndpoints(files); err == nil {
		endpoints = append(endpoints, eps...)
	}
	if eps, err := DetectFastAPIEndpoints(files); err == nil {
		endpoints = append(endpoints, eps...)
	}
	if eps, err := DetectGinEndpoints(files); err == nil {
		endpoints = append(endpoints, eps...)
	}
	if eps, err := DetectNodeEndpoints(files); err == nil {
		endpoints = append(endpoints, eps...)
	}

	return endpoints
}

// expandRouteMethods resolves a framework's catch-all route helper (Gin's
// Any, Express/Koa's app.all) into concrete verbs. Emitting a literal "ANY"
// method would make http.NewRequest fail at send time.
func expandRouteMethods(method string) []string {
	switch method {
	case "Any", "all", "ANY":
		return []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	}
	if m, ok := ginSupportedMethods[method]; ok {
		return []string{m}
	}
	return nil
}
