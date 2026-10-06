package frameworks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGinRouteDetection(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gin-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	goCode := `package main

import (
	"github.com/gin-gonic/gin"
)

func main() {
	router := gin.Default()

	router.GET("/ping", handlePing)
	router.POST("/submit", authMiddleware, handleSubmit)

	v1 := router.Group("/api/v1")
	{
		v1.POST("/games", handlers.CreateGame)
		v1.POST("/games/:id/join", handlers.JoinGame)
		v1.GET("/games/:id", handlers.GetGameState)
		v1.DELETE("/games/:id", handlers.DeleteGame)

		auth := v1.Group("/auth")
		auth.POST("/login", handleLogin)
	}

	router.Static("/static", "./static")
	router.StaticFile("/favicon.ico", "./static/favicon.ico")

	router.GET("/inline", func(c *gin.Context) {
		c.String(200, "ok")
	})
}
`
	tmpFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(tmpFile, []byte(goCode), 0644); err != nil {
		t.Fatalf("failed to write test go file: %v", err)
	}

	if err := os.MkdirAll("queries", 0755); err != nil {
		t.Fatalf("failed to create queries dir: %v", err)
	}
	qData, err := os.ReadFile("../../queries/gin.scm")
	if err != nil {
		t.Fatalf("failed to read source query file: %v", err)
	}
	if err := os.WriteFile("queries/gin.scm", qData, 0644); err != nil {
		t.Fatalf("failed to write package queries/gin.scm: %v", err)
	}
	defer os.RemoveAll("queries")

	endpoints, err := DetectGinEndpoints([]string{tmpFile})
	if err != nil {
		t.Fatalf("endpoint detection failed: %v", err)
	}

	if len(endpoints) != 10 {
		t.Fatalf("expected 10 endpoints, got %d", len(endpoints))
	}

	// Verify specific routes
	routeMap := make(map[string]Endpoint)
	for _, ep := range endpoints {
		key := ep.Method + " " + ep.Path
		routeMap[key] = ep
	}

	// 1. Direct GET
	if ep, ok := routeMap["GET /ping"]; !ok {
		t.Errorf("missing GET /ping")
	} else if ep.HandlerFunc != "handlePing" {
		t.Errorf("expected handler 'handlePing', got %q", ep.HandlerFunc)
	}

	// 2. Direct POST with middleware
	if ep, ok := routeMap["POST /submit"]; !ok {
		t.Errorf("missing POST /submit")
	} else if ep.HandlerFunc != "handleSubmit" {
		t.Errorf("expected handler 'handleSubmit', got %q", ep.HandlerFunc)
	}

	// 3. Grouped route with param
	if ep, ok := routeMap["POST /api/v1/games/:id/join"]; !ok {
		t.Errorf("missing POST /api/v1/games/:id/join")
	} else {
		if ep.HandlerFunc != "handlers.JoinGame" {
			t.Errorf("expected handler 'handlers.JoinGame', got %q", ep.HandlerFunc)
		}
		if len(ep.PathParams) != 1 || ep.PathParams[0].Name != "id" {
			t.Errorf("expected 1 path param 'id', got: %+v", ep.PathParams)
		}
	}

	// 4. Nested group route
	if ep, ok := routeMap["POST /api/v1/auth/login"]; !ok {
		t.Errorf("missing POST /api/v1/auth/login")
	} else if ep.HandlerFunc != "handleLogin" {
		t.Errorf("expected handler 'handleLogin', got %q", ep.HandlerFunc)
	}

	// 5. Static file
	if ep, ok := routeMap["GET /static"]; !ok {
		t.Errorf("missing GET /static")
	} else if ep.HandlerFunc != "gin.Static(./static)" {
		t.Errorf("expected handler 'gin.Static(./static)', got %q", ep.HandlerFunc)
	}

	// 6. Inline func
	if ep, ok := routeMap["GET /inline"]; !ok {
		t.Errorf("missing GET /inline")
	} else if ep.Framework != "gin" {
		t.Errorf("expected framework 'gin', got %q", ep.Framework)
	}
}

func TestGinRouteWithConstants(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "gin-const-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	goCode := `package main

import (
	"github.com/gin-gonic/gin"
)

const (
	ApiPrefix = "/api/v2"
	UsersPath = "/users/:id"
)

const HealthPath = "/healthz"

func main() {
	r := gin.New()

	r.GET(HealthPath, handleHealth)

	api := r.Group(ApiPrefix)
	api.GET(UsersPath, handleGetUser)

	var subPath = "/profile"
	api.POST(subPath, handleProfile)
}
`
	tmpFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(tmpFile, []byte(goCode), 0644); err != nil {
		t.Fatalf("failed to write test go file: %v", err)
	}

	if err := os.MkdirAll("queries", 0755); err != nil {
		t.Fatalf("failed to create queries dir: %v", err)
	}
	qData, err := os.ReadFile("../../queries/gin.scm")
	if err != nil {
		t.Fatalf("failed to read source query file: %v", err)
	}
	if err := os.WriteFile("queries/gin.scm", qData, 0644); err != nil {
		t.Fatalf("failed to write package queries/gin.scm: %v", err)
	}
	defer os.RemoveAll("queries")

	endpoints, err := DetectGinEndpoints([]string{tmpFile})
	if err != nil {
		t.Fatalf("endpoint detection failed: %v", err)
	}

	if len(endpoints) != 3 {
		t.Fatalf("expected 3 endpoints, got %d", len(endpoints))
	}

	routeMap := make(map[string]Endpoint)
	for _, ep := range endpoints {
		routeMap[ep.Method+" "+ep.Path] = ep
	}

	if ep, ok := routeMap["GET /healthz"]; !ok {
		t.Errorf("missing GET /healthz")
	} else if ep.HandlerFunc != "handleHealth" {
		t.Errorf("expected handleHealth, got %q", ep.HandlerFunc)
	}

	if ep, ok := routeMap["GET /api/v2/users/:id"]; !ok {
		t.Errorf("missing GET /api/v2/users/:id")
	} else if len(ep.PathParams) != 1 || ep.PathParams[0].Name != "id" {
		t.Errorf("expected param 'id', got: %+v", ep.PathParams)
	}

	if _, ok := routeMap["POST /api/v2/profile"]; !ok {
		t.Errorf("missing POST /api/v2/profile")
	}
}
