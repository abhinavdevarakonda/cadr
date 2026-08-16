package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abhinavdevarakonda/cadr/internal/analyzer"
	"github.com/abhinavdevarakonda/cadr/internal/frameworks"
	"github.com/abhinavdevarakonda/cadr/internal/graph"
	"github.com/abhinavdevarakonda/cadr/internal/tui"
)

func runAPICmd(path string, globalMode bool) {
	var endpoints []frameworks.Endpoint
	var g *graph.Graph

	startGlobal := globalMode

	if !globalMode {
		var err error
		endpoints, err = loadDynamicEndpoints(path)
		if err == nil && len(endpoints) > 0 {
			fmt.Printf("Loaded %d endpoints from dynamic cache.\n", len(endpoints))
		} else {
			scan, err := analyzer.Scan(path)
			if err == nil {
				flaskEps, err := frameworks.DetectFlaskEndpoints(scan.Files)
				if err == nil {
					endpoints = append(endpoints, flaskEps...)
				}
				fastapiEps, err := frameworks.DetectFastAPIEndpoints(scan.Files)
				if err == nil {
					endpoints = append(endpoints, fastapiEps...)
				}
			}
		}

		if len(endpoints) == 0 {
			startGlobal = true
		} else {
			result := analyzer.Analyze(path)
			g = result.Graph
		}
	}

	apiConfig := loadAPIConfig(path)
	if err := tui.StartAPI(endpoints, apiConfig, g, startGlobal); err != nil {
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

func runAPIListCmd(args []string) {
	local := false
	for _, arg := range args {
		if arg == "--local" || arg == "-l" {
			local = true
			break
		}
	}

	if local {
		runLocalAPIList()
		return
	}

	runGlobalAPIList()
}

func runLocalAPIList() {
	endpoints, err := loadDynamicEndpoints(".")
	if err != nil || len(endpoints) == 0 {
		scan, err := analyzer.Scan(".")
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
		fmt.Println("No local endpoints discovered in current directory.")
		return
	}

	apiConfig := loadAPIConfig(".")
	fmt.Println("Local Codebase Endpoints:")
	fmt.Printf("Base URL: %s\n", apiConfig.DefaultURL)
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Printf("%-8s %-40s %s\n", "METHOD", "PATH", "FILE:LINE")
	fmt.Println("--------------------------------------------------------------------------------")
	for _, ep := range endpoints {
		fileLine := fmt.Sprintf("%s:%d", ep.File, ep.Line)
		if ep.File == "" {
			fileLine = "-"
		}
		path := ep.Path
		if len(path) > 40 {
			path = path[:37] + "..."
		}
		fmt.Printf("%-8s %-40s %s\n", ep.Method, path, fileLine)
	}
	fmt.Println("--------------------------------------------------------------------------------")
}

func runGlobalAPIList() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	extPath := filepath.Join(home, ".cadr", "external_endpoints.json")
	data, err := os.ReadFile(extPath)
	if err != nil {
		fmt.Println("No global endpoints registered yet.")
		return
	}

	var records []ExternalEndpointRecord
	if err := json.Unmarshal(data, &records); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if len(records) == 0 {
		fmt.Println("No global endpoints registered yet.")
		return
	}

	fmt.Println("Global External Endpoints:")
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Printf("%-20s %-8s %s\n", "ALIAS/NAME", "METHOD", "URL")
	fmt.Println("--------------------------------------------------------------------------------")
	for _, r := range records {
		name := r.Name
		if name == "" {
			name = "-"
		}
		urlStr := r.Path
		if len(urlStr) > 45 {
			urlStr = urlStr[:42] + "..."
		}
		fmt.Printf("%-20s %-8s %s\n", name, r.Method, urlStr)
	}
	fmt.Println("--------------------------------------------------------------------------------")
}

func runAPIDeleteCmd(args []string) {
	if len(args) == 0 {
		fmt.Println("Error: missing identifier. Usage: cadr api delete <NAME> or cadr api delete <METHOD> <URL>")
		os.Exit(1)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	extPath := filepath.Join(home, ".cadr", "external_endpoints.json")
	data, err := os.ReadFile(extPath)
	if err != nil {
		fmt.Println("No global endpoints registered.")
		return
	}

	var records []ExternalEndpointRecord
	if err := json.Unmarshal(data, &records); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	var newRecords []ExternalEndpointRecord
	found := false
	var deletedName, deletedMethod, deletedURL string

	if len(args) == 1 {
		targetName := args[0]
		for _, r := range records {
			if r.Name == targetName && targetName != "" {
				found = true
				deletedName = r.Name
				deletedMethod = r.Method
				deletedURL = r.Path
			} else {
				newRecords = append(newRecords, r)
			}
		}
	} else {
		method := strings.ToUpper(args[0])
		urlStr := args[1]
		for _, r := range records {
			if r.Method == method && r.Path == urlStr {
				found = true
				deletedName = r.Name
				deletedMethod = r.Method
				deletedURL = r.Path
			} else {
				newRecords = append(newRecords, r)
			}
		}
	}

	if !found {
		fmt.Println("Error: endpoint not found in global registry.")
		os.Exit(1)
	}

	newData, err := json.MarshalIndent(newRecords, "", "  ")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(extPath, newData, 0644); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if deletedName != "" {
		fmt.Printf("Successfully deleted global endpoint: %s (%s %s)\n", deletedName, deletedMethod, deletedURL)
	} else {
		fmt.Printf("Successfully deleted global endpoint: %s %s\n", deletedMethod, deletedURL)
	}
}
