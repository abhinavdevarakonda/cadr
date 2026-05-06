package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/abhinavdevarakonda/cadastre/internal/agents"
	"github.com/abhinavdevarakonda/cadastre/internal/analyzer"
	"github.com/abhinavdevarakonda/cadastre/internal/graph"
	_ "github.com/abhinavdevarakonda/cadastre/internal/lang/c"
	_ "github.com/abhinavdevarakonda/cadastre/internal/lang/golang"
	_ "github.com/abhinavdevarakonda/cadastre/internal/lang/javascript"
	_ "github.com/abhinavdevarakonda/cadastre/internal/lang/python"
	"github.com/abhinavdevarakonda/cadastre/internal/server"
	"github.com/abhinavdevarakonda/cadastre/internal/tracer"
	"github.com/abhinavdevarakonda/cadastre/internal/tui"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

var _ = agents.DetectLanguage // reference to avoid unused import

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage:")
		fmt.Println("	cadr analyze <path>")
		fmt.Println("	cadr export <path>")
		fmt.Println("	cadr serve <path>")
		fmt.Println("	cadr mcp <path>")
		fmt.Println("	cadr nav <path>")
		fmt.Println("	cadr flow <path>")
		fmt.Println("	cadr run <command>")
		fmt.Println("	cadr rec <command>")
		return
	}

	command := os.Args[1]

	path := "."
	if len(os.Args) > 2 {
		path = os.Args[2]
	}

	switch command {
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

		fmt.Println("Cadastre Analysis Summary")
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

	case "nav", "navigate", "tui":
		result := analyzer.Analyze(path)
		if err := tui.Start(result.Graph); err != nil {
			panic(err)
		}

	case "flow":
		// static analysis
		result := analyzer.Analyze(path)

		target := ""
		if len(os.Args) >= 4 {
			target = os.Args[3]
		}

		// start monitor TUI
		if err := tui.StartMonitor(result.Graph, target); err != nil {
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
		if len(os.Args) < 3 {
			fmt.Println("Usage: cadr run <command>")
			return
		}
		cmdStr := os.Args[2]
		// We don't need a callback here because the tracer itself
		// (if it's our py_trace) will connect to the local socket
		// server started by 'cadr flow'.
		if err := tracer.Run(cmdStr, func(e tracer.Event) {
			// Fallback: If socket fails, we still see something here
			fmt.Fprintf(os.Stderr, " [TRACE FALLBACK] %s\n", e.Name)
		}); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

	case "rec":
		if len(os.Args) < 3 {
			fmt.Println("Usage: cadr rec <command>")
			return
		}
		cmdStr := os.Args[2]

		// Ensure .cadr directory exists
		if err := os.MkdirAll(".cadr", 0755); err != nil {
			fmt.Printf("Error creating .cadr dir: %v\n", err)
			os.Exit(1)
		}

		outFile, err := os.Create(".cadr/last_run.jsonl")
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

		fmt.Fprintf(os.Stderr, "Recording trace to .cadr/last_run.jsonl...\n")
		if err := tracer.Run(cmdStr, func(e tracer.Event) {}); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		ln.Close()
		fmt.Fprintf(os.Stderr, "Trace saved to .cadr/last_run.jsonl\n")

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
