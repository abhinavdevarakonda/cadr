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

type ExternalEndpointRecord struct {
	Name         string   `json:"name,omitempty"`
	Method       string   `json:"method"`
	Path         string   `json:"path"`
	SavedParams  []string `json:"saved_params,omitempty"`
	SavedQuery   string   `json:"saved_query,omitempty"`
	SavedHeaders string   `json:"saved_headers,omitempty"`
	SavedBody    string   `json:"saved_body,omitempty"`
	HitCount     int      `json:"hit_count,omitempty"`
	LastCalled   string   `json:"last_called,omitempty"`
}

func runAPIAddCmd(args []string) {
	if len(args) == 0 {
		fmt.Println("Error: missing endpoint URL. Usage: cadr api add [METHOD] <URL> --name <NAME>")
		os.Exit(1)
	}

	method := "GET"
	urlStr := ""
	name := ""

	// Parse flags manually
	var cleanArgs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--name" || args[i] == "-n" {
			if i+1 < len(args) {
				name = args[i+1]
				i++
			} else {
				fmt.Println("Error: missing value for --name flag")
				os.Exit(1)
			}
		} else {
			cleanArgs = append(cleanArgs, args[i])
		}
	}

	if len(cleanArgs) == 0 {
		fmt.Println("Error: missing endpoint URL. Usage: cadr api add [METHOD] <URL> --name <NAME>")
		os.Exit(1)
	}

	if len(cleanArgs) == 1 {
		urlStr = cleanArgs[0]
	} else {
		method = strings.ToUpper(cleanArgs[0])
		urlStr = cleanArgs[1]
	}

	// Validate method
	validMethods := map[string]bool{
		"GET": true, "POST": true, "PUT": true, "DELETE": true,
		"PATCH": true, "OPTIONS": true, "HEAD": true,
	}
	if !validMethods[method] {
		if strings.HasPrefix(cleanArgs[0], "http") {
			urlStr = cleanArgs[0]
			method = "GET"
		} else {
			fmt.Printf("Error: invalid HTTP method '%s'\n", method)
			os.Exit(1)
		}
	}

	err := addExternalEndpoint(method, urlStr, name)
	if err != nil {
		fmt.Printf("Error saving external endpoint: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully added global endpoint: %s %s (alias: %s)\n", method, urlStr, name)
}

func addExternalEndpoint(method, urlStr, name string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	cadrDir := filepath.Join(home, ".cadr")
	_ = os.MkdirAll(cadrDir, 0755)
	extPath := filepath.Join(cadrDir, "external_endpoints.json")

	var records []ExternalEndpointRecord
	data, err := os.ReadFile(extPath)
	if err == nil {
		_ = json.Unmarshal(data, &records)
	}

	// Check if already exists, overwrite if matching method and path
	found := false
	for i, r := range records {
		if r.Method == method && r.Path == urlStr {
			records[i].Name = name
			found = true
			break
		}
	}

	if !found {
		records = append(records, ExternalEndpointRecord{
			Name:   name,
			Method: method,
			Path:   urlStr,
		})
	}

	newData, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(extPath, newData, 0644)
}
