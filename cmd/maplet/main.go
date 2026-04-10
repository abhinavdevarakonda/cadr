package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/abhinavdevarakonda/maplet/internal/agents"
	"github.com/abhinavdevarakonda/maplet/internal/analyzer"
	"github.com/abhinavdevarakonda/maplet/internal/graph"
	_ "github.com/abhinavdevarakonda/maplet/internal/lang/c"
	_ "github.com/abhinavdevarakonda/maplet/internal/lang/golang"
	_ "github.com/abhinavdevarakonda/maplet/internal/lang/python"
	"github.com/abhinavdevarakonda/maplet/internal/server"
	"github.com/abhinavdevarakonda/maplet/internal/tracer"
	"github.com/abhinavdevarakonda/maplet/internal/tui"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

var _ = agents.DetectLanguage // reference to avoid unused import

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage:")
		fmt.Println("	maplet analyze <path>")
		fmt.Println("	maplet export <path>")
		fmt.Println("	maplet serve <path>")
		fmt.Println("	maplet mcp <path>")
		fmt.Println("	maplet nav <path>")
		fmt.Println("	maplet flow <path>")
		fmt.Println("	maplet run <command>")
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

		fmt.Println("Maplet Analysis Summary")
		fmt.Println("------------------------")
		fmt.Printf("Functions: %d\n", functionCount)
		fmt.Printf("Call edges: %d\n", callEdgeCount)
		fmt.Printf("Total nodes: %d\n", len(g.Nodes))
		fmt.Printf("Total edges: %d\n", len(g.Edges))

	case "impact":
		if len(os.Args) < 3 {
			fmt.Println("usage: maplet impact [path] <symbol>")
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
		result := analyzer.Analyze(path)
		mcpSrv := server.NewMCPServer(&result)
		stdioSrv := mcpserver.NewStdioServer(mcpSrv)
		if err := stdioSrv.Listen(context.Background(), os.Stdin, os.Stdout); err != nil {
			panic(err)
		}

	case "run":
		if len(os.Args) < 3 {
			fmt.Println("Usage: maplet run <command>")
			return
		}
		cmdStr := os.Args[2]
		// We don't need a callback here because the tracer itself
		// (if it's our py_trace) will connect to the local socket
		// server started by 'maplet flow'.
		if err := tracer.Run(cmdStr, func(e tracer.Event) {
			// Fallback: If socket fails, we still see something here
			fmt.Fprintf(os.Stderr, " [TRACE FALLBACK] %s\n", e.Name)
		}); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}

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
