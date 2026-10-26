package frameworks

import (
	"strings"
	"testing"
)

func detectGoProject(t *testing.T, files map[string]string) []Endpoint {
	t.Helper()
	endpoints, err := DetectGinEndpoints(writeNodeProject(t, files))
	if err != nil {
		t.Fatalf("gin detection failed: %v", err)
	}
	return endpoints
}

func TestGinSiblingFileConstants(t *testing.T) {
	endpoints := detectGoProject(t, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.21\n",
		"main.go": `package main

import "github.com/gin-gonic/gin"

func main() {
	r := gin.Default()
	api := r.Group(ApiPrefix)
	api.GET(UsersPath, handleUsers)
}
`,
		"consts.go": `package main

const (
	ApiPrefix = "/api/v2"
	UsersPath = "/users"
)
`,
	})

	requireRoute(t, endpoints, "GET /api/v2/users")
}

func TestGinCrossPackageConstants(t *testing.T) {
	endpoints := detectGoProject(t, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.21\n",
		"internal/config/config.go": `package config

const Prefix = "/api/cfg"
const AdminPrefix = "/admin"
`,
		"main.go": `package main

import (
	"github.com/gin-gonic/gin"
	"example.com/app/internal/config"
)

func main() {
	r := gin.Default()
	r.GET(config.Prefix+"/users", handleUsers)
}
`,
		"admin.go": `package main

import (
	"github.com/gin-gonic/gin"
	cfg "example.com/app/internal/config"
)

func adminRoutes(r *gin.Engine) {
	api := r.Group(cfg.AdminPrefix)
	api.GET("/stats", handleStats)
}
`,
	})

	requireRoute(t, endpoints, "GET /api/cfg/users")
	requireRoute(t, endpoints, "GET /admin/stats")
}

func TestGinGroupPrefixConstant(t *testing.T) {
	endpoints := detectGoProject(t, map[string]string{
		"main.go": `package main

import "github.com/gin-gonic/gin"

const APIPrefix = "/api/v3"

func main() {
	r := gin.Default()
	api := r.Group(APIPrefix)
	api.GET("/health", handleHealth)
}
`,
	})

	requireRoute(t, endpoints, "GET /api/v3/health")
}

func TestGinUnresolvedGroupPrefixBecomesFillable(t *testing.T) {
	endpoints := detectGoProject(t, map[string]string{
		"main.go": `package main

import (
	"os"

	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.Default()
	api := r.Group(os.Getenv("API_PREFIX"))
	api.GET("/books", handleBooks)
}
`,
	})

	var found bool
	for _, ep := range endpoints {
		if strings.HasSuffix(ep.Path, "/books") && strings.Contains(ep.Path, "exprL") {
			found = true
			if len(ep.PathParams) != 1 || ep.PathParams[0].Type != UnresolvedPathParamType {
				t.Errorf("expected an unresolved prefix param, got %+v", ep.PathParams)
			}
		}
	}
	if !found {
		t.Fatalf("expected a fillable group prefix, got %+v", endpoints)
	}
}

func TestGinUnresolvedRoutePathBecomesFillable(t *testing.T) {
	endpoints := detectGoProject(t, map[string]string{
		"main.go": `package main

import (
	"os"

	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.Default()
	r.GET(os.Getenv("BOOKS_PATH"), handleBooks)
	r.GET(config.UnknownPath, handleOther)
}
`,
	})

	if len(endpoints) != 2 {
		t.Fatalf("expected both routes to be listed, got %+v", endpoints)
	}
	for _, ep := range endpoints {
		if len(ep.PathParams) != 1 || ep.PathParams[0].Type != UnresolvedPathParamType {
			t.Errorf("expected an unresolved param on %s, got %+v", ep.Path, ep.PathParams)
		}
	}
	var sawSelector bool
	for _, ep := range endpoints {
		if strings.Contains(ep.Path, "[config.UnknownPath]") {
			sawSelector = true
		}
	}
	if !sawSelector {
		t.Errorf("expected the unresolved selector to keep its name, got %+v", endpoints)
	}
}

func TestGoIndexWithoutGoModDoesNotPanic(t *testing.T) {
	paths := writeNodeProject(t, map[string]string{
		"main.go": `package main

import "github.com/gin-gonic/gin"

func main() {
	r := gin.Default()
	r.GET("/ping", handlePing)
}
`,
	})

	index := newGoIndex(paths, "")
	if index.modulePath != "" {
		t.Fatalf("expected no module path without go.mod, got %q", index.modulePath)
	}
	endpoints, err := DetectGinEndpoints(paths)
	if err != nil {
		t.Fatalf("gin detection failed: %v", err)
	}
	requireRoute(t, endpoints, "GET /ping")
}

// The reported bug: the group prefix is a config field (`cfg.APIBase`) whose
// default only exists as a struct literal in another package. The default is
// used, and Gin's empty relative path stays at the group base (no trailing
// slash).
func TestGinResolvesConfigFieldDefault(t *testing.T) {
	endpoints := detectGoProject(t, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.21\n",
		"internal/settings/settings.go": `package settings

const DefaultAPIBase = "/api/v1"

type Config struct {
	APIBase string
}

func LoadConfig() Config {
	return Config{APIBase: DefaultAPIBase}
}
`,
		"internal/httpapi/router.go": `package httpapi

import (
	"github.com/gin-gonic/gin"

	"example.com/app/internal/settings"
)

func NewRouter(cfg settings.Config) *gin.Engine {
	router := gin.New()
	api := router.Group(cfg.APIBase)
	books := api.Group("/books")
	books.GET("", HandleListBooks)
	books.POST("/batch", HandleCreateBooksBatch)
	return router
}
`,
	})

	ep := requireRoute(t, endpoints, "GET /api/v1/books")
	if len(ep.PathParams) != 0 {
		t.Errorf("expected no unresolved params on the collection route, got %+v", ep.PathParams)
	}
	requireRoute(t, endpoints, "POST /api/v1/books/batch")
}

func TestGinAmbiguousFieldDefaultKeepsPlaceholder(t *testing.T) {
	endpoints := detectGoProject(t, map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.21\n",
		"internal/settings/settings.go": `package settings

type Config struct {
	APIBase string
}

var first = Config{APIBase: "/one"}
var second = Config{APIBase: "/two"}
`,
		"internal/httpapi/router.go": `package httpapi

import (
	"github.com/gin-gonic/gin"

	"example.com/app/internal/settings"
)

func NewRouter(cfg settings.Config) *gin.Engine {
	router := gin.New()
	api := router.Group(cfg.APIBase)
	books := api.Group("/books")
	books.GET("", HandleListBooks)
	return router
}
`,
	})

	ep := requireRoute(t, endpoints, "GET /[cfg.APIBase]/books")
	if len(ep.PathParams) != 1 || ep.PathParams[0].Name != "cfg.APIBase" || ep.PathParams[0].Type != UnresolvedPathParamType {
		t.Errorf("expected one fillable cfg.APIBase param, got %+v", ep.PathParams)
	}
}
