package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abhinavdevarakonda/cadr/internal/analyzer"
	"github.com/abhinavdevarakonda/cadr/internal/frameworks"
	"github.com/abhinavdevarakonda/cadr/internal/tui"
)

func runAPICmd(path string) {
	// Try loading from dynamic endpoints cache first
	endpoints, err := loadDynamicEndpoints(path)
	if err == nil && len(endpoints) > 0 {
		fmt.Printf("Loaded %d endpoints from dynamic cache.\n", len(endpoints))
	} else {
		scan, err := analyzer.Scan(path)
		if err != nil {
			fmt.Printf("Error scanning project: %v\n", err)
			os.Exit(1)
		}

		endpoints, err = frameworks.DetectFlaskEndpoints(scan.Files)
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
	}

	if len(endpoints) == 0 {
		fmt.Println("No endpoints discovered. Try running your app under 'cadr rec' to dynamically harvest routes.")
		return
	}

	// Build the static call graph
	result := analyzer.Analyze(path)

	apiConfig := loadAPIConfig(path)
	if err := tui.StartAPI(endpoints, apiConfig, result.Graph); err != nil {
		fmt.Printf("TUI Error: %v\n", err)
		os.Exit(1)
	}
}

func loadDynamicEndpoints(root string) ([]frameworks.Endpoint, error) {
	cachePath := filepath.Join(root, ".cadr", "cache", "endpoints.json")
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, err
	}
	var endpoints []frameworks.Endpoint
	if err := json.Unmarshal(data, &endpoints); err != nil {
		return nil, err
	}
	return endpoints, nil
}

func loadAPIConfig(root string) tui.APIConfig {
	cfg := tui.APIConfig{
		DefaultURL: "http://localhost:5000",
		FlaskURL:   "http://localhost:5000",
		FastAPIURL: "http://localhost:8081",
	}

	cfgPath := filepath.Join(root, ".cadr", "config.yaml")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		cfgPath = filepath.Join(root, "cadr.yaml")
	}

	data, err := os.ReadFile(cfgPath)
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
