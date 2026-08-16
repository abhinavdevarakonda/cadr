package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/abhinavdevarakonda/cadr/internal/agents"
	"github.com/abhinavdevarakonda/cadr/internal/analyzer"
	"github.com/abhinavdevarakonda/cadr/internal/graph"
	_ "github.com/abhinavdevarakonda/cadr/internal/lang/c"
	_ "github.com/abhinavdevarakonda/cadr/internal/lang/golang"
	_ "github.com/abhinavdevarakonda/cadr/internal/lang/javascript"
	_ "github.com/abhinavdevarakonda/cadr/internal/lang/python"
	"github.com/abhinavdevarakonda/cadr/internal/server"
	"github.com/abhinavdevarakonda/cadr/internal/tracer"
	"github.com/abhinavdevarakonda/cadr/internal/tui"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

var _ = agents.DetectLanguage // reference to avoid unused import

func main() {
	// Parse and strip global bypass flags
	for _, arg := range os.Args {
		if arg == "-y" || arg == "--yes" {
			analyzer.BypassPrompt = true
		}
	}

	var cleanArgs []string
	for _, arg := range os.Args {
		if arg != "-y" && arg != "--yes" {
			cleanArgs = append(cleanArgs, arg)
		}
	}
	os.Args = cleanArgs

	// cadr or cadr <path> → open TUI directly
	if len(os.Args) < 2 {
		result := analyzer.Analyze(".")
		if err := tui.Start(result.Graph, "."); err != nil {
			panic(err)
		}
		return
	}

	command := os.Args[1]

	path := "."
	if len(os.Args) > 2 {
		path = os.Args[2]
	}

	// if the first arg looks like a path (not a known subcommand), open TUI on it
	knownCommands := map[string]bool{
		"analyze": true, "impact": true, "export": true,
		"serve": true, "mcp": true, "run": true, "rec": true,
		"api":     true,
		"version": true, "--version": true, "-v": true,
	}
	if !knownCommands[command] {
		result := analyzer.Analyze(command)
		if err := tui.Start(result.Graph, command); err != nil {
			panic(err)
		}
		return
	}

	switch command {
	case "api":
		if len(os.Args) > 2 {
			sub := os.Args[2]
			if sub == "add" || sub == "list" || sub == "delete" || sub == "rm" {
				switch sub {
				case "add":
					runAPIAddCmd(os.Args[3:])
				case "list":
					runAPIListCmd(os.Args[3:])
				case "delete", "rm":
					runAPIDeleteCmd(os.Args[3:])
				}
				return
			}
		}

		globalMode := false
		targetPath := "."
		for i := 2; i < len(os.Args); i++ {
			arg := os.Args[i]
			if arg == "--global" || arg == "-g" {
				globalMode = true
			} else if !strings.HasPrefix(arg, "-") {
				targetPath = arg
			}
		}
		runAPICmd(targetPath, globalMode)

	case "analyze":
		result := analyzer.Analyze(path)
		g := result.Graph

		var functionCount int
		var callEdgeCount int

		for _, n := range g.Nodes {
			if n.Type == graph.FunctionNode {
				functionCount++
			}
		}

		for _, e := range g.Edges {
			if e.Type == graph.CallsEdge {
				callEdgeCount++
			}
		}

		fmt.Println("cadr Analysis Summary")
		fmt.Println("------------------------")
		fmt.Printf("Functions: %d\n", functionCount)
		fmt.Printf("Call edges: %d\n", callEdgeCount)
		fmt.Printf("Total nodes: %d\n", len(g.Nodes))
		fmt.Printf("Total edges: %d\n", len(g.Edges))

	case "impact":
		if len(os.Args) < 3 {
			fmt.Println("usage: cadr impact [path] <symbol>")
			return
		}

		var path string
		var rawInput string

		if len(os.Args) == 3 {
			path = "."
			rawInput = os.Args[2]
		} else {
			path = os.Args[2]
			rawInput = os.Args[3]
		}

		result := analyzer.Analyze(path)
		g := result.Graph

		resolvedId, err := resolveSymbol(g, rawInput)
		if err != nil {
			fmt.Println(err)
			return
		}

		impacted := analyzer.ImpactAnalysis(g, resolvedId)

		if len(impacted) == 0 {
			fmt.Println("no impacted functions.")
			return
		}

		fmt.Println("Impacted functions:")
		for _, fn := range impacted {
			fmt.Printf("  %s (line %d)\n", fn.ID, fn.Line)
		}

	case "export":
		result := analyzer.Analyze(path)

		data, err := json.MarshalIndent(result.Graph, "", " ")
		if err != nil {
			panic(err)
		}
		fmt.Println(string(data))

	case "serve":
		result := analyzer.Analyze(path)

		srv := server.New(result.Graph)
		if err := srv.Start("localhost:6433"); err != nil {
			panic(err)
		}

	case "mcp":
		// empty result so we can start the server immediately
		result := &analyzer.Result{Root: path}
		mcpSrv := server.NewMCPServer(result)
		stdioSrv := mcpserver.NewStdioServer(mcpSrv)

		// run analysis in background so it doesn't block server startup
		go func() {
			fullResult := analyzer.Analyze(path)
			*result = fullResult
		}()

		if err := stdioSrv.Listen(context.Background(), os.Stdin, os.Stdout); err != nil {
			panic(err)
		}

	case "run":
		var langOverride string
		cmdIdx := 2
		if len(os.Args) >= 4 && (os.Args[2] == "-l" || os.Args[2] == "--lang" || os.Args[2] == "--language") {
			langOverride = os.Args[3]
			cmdIdx = 4
		}

		var cmdStr string
		if len(os.Args) > cmdIdx {
			cmdStr = os.Args[cmdIdx]
		} else {
			var cfgLang string
			cmdStr, cfgLang = loadDefaultRunCmd(".")
			if langOverride == "" {
				langOverride = cfgLang
			}
		}

		if cmdStr == "" {
			fmt.Println("Usage: cadr run [-l <language>] [<command>]")
			fmt.Println("Error: No command specified, and no 'run_cmd' found in cadr.yaml.")
			return
		}

		// We don't need a callback here because the tracer itself
		// (if it's our py_trace) will connect to the local socket
		// server started by 'cadr flow'.
		if err := tracer.RunWithLang(cmdStr, langOverride, func(e tracer.Event) {
			// Fallback: If socket fails, we still see something here
			fmt.Fprintf(os.Stderr, " [TRACE FALLBACK] %s\n", e.Name)
		}); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

	case "rec":
		var langOverride string
		cmdIdx := 2
		if len(os.Args) >= 4 && (os.Args[2] == "-l" || os.Args[2] == "--lang" || os.Args[2] == "--language") {
			langOverride = os.Args[3]
			cmdIdx = 4
		}

		var cmdStr string
		if len(os.Args) > cmdIdx {
			cmdStr = os.Args[cmdIdx]
		} else {
			var cfgLang string
			cmdStr, cfgLang = loadDefaultRunCmd(".")
			if langOverride == "" {
				langOverride = cfgLang
			}
		}

		if cmdStr == "" {
			fmt.Println("Usage: cadr rec [-l <language>] [<command>]")
			fmt.Println("Error: No command specified, and no 'run_cmd' found in cadr.yaml.")
			return
		}

		// Ensure .cadr/traces directory exists
		if err := os.MkdirAll(filepath.Join(".cadr", "traces"), 0755); err != nil {
			fmt.Printf("Error creating .cadr/traces dir: %v\n", err)
			os.Exit(1)
		}

		outFile, err := os.Create(filepath.Join(".cadr", "traces", "last_run.jsonl"))
		if err != nil {
			fmt.Printf("Error creating record file: %v\n", err)
			os.Exit(1)
		}
		defer outFile.Close()
		writer := bufio.NewWriter(outFile)
		var mu sync.Mutex

		// Start TCP listener so the agent can connect
		ln, err := net.Listen("tcp", "localhost:9876")
		if err != nil {
			fmt.Printf("Error starting listener: %v\n", err)
			os.Exit(1)
		}
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				go func(c net.Conn) {
					defer c.Close()
					scanner := bufio.NewScanner(c)
					for scanner.Scan() {
						mu.Lock()
						_, _ = writer.WriteString(scanner.Text() + "\n")
						_ = writer.Flush()
						mu.Unlock()
					}
				}(conn)
			}
		}()

		fmt.Fprintf(os.Stderr, "Recording trace to .cadr/traces/last_run.jsonl...\n")
		if err := tracer.RunWithLang(cmdStr, langOverride, func(e tracer.Event) {}); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		ln.Close()
		fmt.Fprintf(os.Stderr, "Trace saved to .cadr/traces/last_run.jsonl\n")

	case "version", "--version", "-v":
		fmt.Println("cadr version 0.4.0")

	default:
		fmt.Println("unknown command:", command)
	}
}

func resolveSymbol(g *graph.Graph, input string) (string, error) {
	if _, exists := g.Nodes[input]; exists {
		return input, nil
	}

	var matches []string

	for id, node := range g.Nodes {
		if node.Type != graph.FunctionNode {
			continue
		}

		if node.Name == input {
			matches = append(matches, id)
		}
	}

	if len(matches) == 1 {
		return matches[0], nil
	}

	if len(matches) > 1 {
		return "", fmt.Errorf(
			"ambiguous symbol %q. Possible matches:\n %s",
			input,
			strings.Join(matches, "\n  "),
		)
	}

	return "", fmt.Errorf("symbol %q not found", input)
}

func loadDefaultRunCmd(root string) (string, string) {
	cfgPath := filepath.Join(root, ".cadr", "config.yaml")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		cfgPath = filepath.Join(root, "cadr.yaml")
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return "", ""
	}

	var runCmd, runLang string
	lines := strings.Split(string(data), "\n")
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

		if key == "run_cmd" {
			runCmd = val
		} else if key == "run_lang" {
			runLang = val
		}
	}
	return runCmd, runLang
}
