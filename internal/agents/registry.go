package agents

import (
	"embed"
	"os"
	"path/filepath"
)

type Agent struct {
	Name          string
	Files         embed.FS
	EnvVar        string
	EnvValue      string
	TraceEnvVar   string
	TraceEnvValue string
}

var registry = make(map[string]Agent)

func Register(name string, agent Agent) {
	registry[name] = agent
}

func Get(name string) (Agent, bool) {
	agent, ok := registry[name]
	return agent, ok
}

func DetectLanguage(cmd string) string {
	parts := splitCommand(cmd)
	if len(parts) == 0 {
		return ""
	}
	cmdName := parts[0]

	switch cmdName {
	case "python", "python3", "py":
		return "python"
	case "node", "nodejs", "npm":
		return "javascript"
	case "ruby", "rb":
		return "ruby"
	case "go":
		return "go"
	case "cargo":
		return "rust"
	case "java":
		return "java"
	case "javac":
		return "java"
	case "dotnet":
		return "csharp"
	}
	return ""
}

func splitCommand(cmd string) []string {
	var parts []string
	var current []byte
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if !inQuote && (c == '"' || c == '\'') {
			inQuote = true
			quoteChar = c
		} else if inQuote && c == quoteChar {
			inQuote = false
		} else if !inQuote && c == ' ' {
			if len(current) > 0 {
				parts = append(parts, string(current))
				current = nil
			}
		} else {
			current = append(current, c)
		}
	}
	if len(current) > 0 {
		parts = append(parts, string(current))
	}
	return parts
}

func SetupHookDir(agent Agent) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	hookDir := filepath.Join(home, ".maplet", "hooks", agent.Name)
	if err := os.MkdirAll(hookDir, 0755); err != nil {
		return "", err
	}

	entries, err := agent.Files.ReadDir(".")
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		content, err := agent.Files.ReadFile(entry.Name())
		if err != nil {
			return "", err
		}

		destPath := filepath.Join(hookDir, entry.Name())
		if err := os.WriteFile(destPath, content, 0644); err != nil {
			return "", err
		}
	}

	return hookDir, nil
}
