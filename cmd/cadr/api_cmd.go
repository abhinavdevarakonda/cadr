package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/abhinavdevarakonda/cadr/internal/analyzer"
	"github.com/abhinavdevarakonda/cadr/internal/frameworks"
	"github.com/abhinavdevarakonda/cadr/internal/tui"
)

func runAPICmd(path string) {
	scan, err := analyzer.Scan(path)
	if err != nil {
		fmt.Printf("Error scanning project: %v\n", err)
		os.Exit(1)
	}

	endpoints, err := frameworks.DetectFlaskEndpoints(scan.Files)
	if err != nil {
		fmt.Printf("Error detecting Flask endpoints: %v\n", err)
		os.Exit(1)
	}

	fastapiEndpoints, err := frameworks.DetectFastAPIEndpoints(scan.Files)
	if err != nil {
		fmt.Printf("Error detecting FastAPI endpoints: %v\n", err)
		os.Exit(1)
	}

	endpoints = append(endpoints, fastapiEndpoints...)

	if len(endpoints) == 0 {
		fmt.Println("No endpoints discovered.")
		return
	}

	// Build the static call graph
	result := analyzer.Analyze(path)

	apiConfig := loadAPIConfig()
	if err := tui.StartAPI(endpoints, apiConfig, result.Graph); err != nil {
		fmt.Printf("TUI Error: %v\n", err)
		os.Exit(1)
	}
}

func loadAPIConfig() tui.APIConfig {
	cfg := tui.APIConfig{
		DefaultURL: "http://localhost:5000",
		FlaskURL:   "http://localhost:5000",
		FastAPIURL: "http://localhost:8081",
	}

	data, err := os.ReadFile("cadr.yaml")
	if err != nil {
		return cfg
	}

	lines := strings.Split(string(data), "\n")
	hasGlobal := false
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "api_url":
			cfg.DefaultURL = val
			hasGlobal = true
		case "flask_api_url":
			cfg.FlaskURL = val
		case "fastapi_api_url":
			cfg.FastAPIURL = val
		}
	}

	if hasGlobal {
		cfg.FlaskURL = cfg.DefaultURL
		cfg.FastAPIURL = cfg.DefaultURL
	}

	return cfg
}
