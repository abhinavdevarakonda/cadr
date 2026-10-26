package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".cadr"), 0755); err != nil {
		t.Fatalf("failed to create .cadr dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".cadr", "config.yaml"), []byte(body), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	return root
}

func TestLoadAPIConfigDefaults(t *testing.T) {
	cfg := loadAPIConfig(t.TempDir())

	if cfg.NodeURL != "http://localhost:3000" {
		t.Errorf("expected default node_api_url, got %q", cfg.NodeURL)
	}
	if cfg.RequestTimeout != 10*time.Second {
		t.Errorf("expected default api_timeout 10s, got %v", cfg.RequestTimeout)
	}
	if cfg.FastAPIURL != "http://localhost:8081" {
		t.Errorf("expected fastapi default 8081, got %q", cfg.FastAPIURL)
	}
}

func TestLoadAPIConfigTimeoutFormats(t *testing.T) {
	cases := map[string]time.Duration{
		"30s":     30 * time.Second,
		"30":      30 * time.Second,
		"30000ms": 30 * time.Second,
		"1m30s":   90 * time.Second,
	}

	for value, want := range cases {
		root := writeConfig(t, "api_timeout: "+value+"\n")
		cfg := loadAPIConfig(root)
		if cfg.RequestTimeout != want {
			t.Errorf("api_timeout %q: expected %v, got %v", value, want, cfg.RequestTimeout)
		}
	}
}

func TestLoadAPIConfigInvalidTimeoutKeepsDefault(t *testing.T) {
	for _, value := range []string{"soon", "0", "-5", ""} {
		root := writeConfig(t, "api_timeout: "+value+"\n")
		cfg := loadAPIConfig(root)
		if cfg.RequestTimeout != 10*time.Second {
			t.Errorf("api_timeout %q: expected fallback 10s, got %v", value, cfg.RequestTimeout)
		}
	}
}

func TestLoadAPIConfigNodeURL(t *testing.T) {
	root := writeConfig(t, "node_api_url: http://localhost:4000\napi_timeout: 45s\n")
	cfg := loadAPIConfig(root)

	if cfg.NodeURL != "http://localhost:4000" {
		t.Errorf("expected node_api_url, got %q", cfg.NodeURL)
	}
	if cfg.RequestTimeout != 45*time.Second {
		t.Errorf("expected 45s, got %v", cfg.RequestTimeout)
	}
	if cfg.FlaskURL != "http://localhost:5000" {
		t.Errorf("other keys must keep their defaults, got flask %q", cfg.FlaskURL)
	}
}

func TestLoadAPIConfigGlobalAPIURLWins(t *testing.T) {
	root := writeConfig(t, "node_api_url: http://localhost:4000\napi_url: http://localhost:9999\n")
	cfg := loadAPIConfig(root)

	if cfg.NodeURL != "http://localhost:9999" {
		t.Errorf("api_url should override node_api_url, got %q", cfg.NodeURL)
	}
	if cfg.FlaskURL != "http://localhost:9999" || cfg.GinURL != "http://localhost:9999" {
		t.Errorf("api_url should override every framework URL, got %+v", cfg)
	}
}

func TestParseAPITimeout(t *testing.T) {
	cases := []struct {
		value string
		want  time.Duration
		ok    bool
	}{
		{"30s", 30 * time.Second, true},
		{"30", 30 * time.Second, true},
		{"30000ms", 30 * time.Second, true},
		{"1500us", 1500 * time.Microsecond, true},
		{"nope", 0, false},
		{"0", 0, false},
		{"-3s", 0, false},
		{"", 0, false},
	}

	for _, tc := range cases {
		got, ok := parseAPITimeout(tc.value)
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseAPITimeout(%q) = (%v, %v), want (%v, %v)", tc.value, got, ok, tc.want, tc.ok)
		}
	}
}
