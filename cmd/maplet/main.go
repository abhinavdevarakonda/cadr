package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/abhinavdevarakonda/maplet/internal/analyzer"
	_ "github.com/abhinavdevarakonda/maplet/internal/lang/c"
	_ "github.com/abhinavdevarakonda/maplet/internal/lang/golang"
	_ "github.com/abhinavdevarakonda/maplet/internal/lang/python"
	"github.com/abhinavdevarakonda/maplet/internal/server"
	"github.com/abhinavdevarakonda/maplet/internal/tui"
	"github.com/abhinavdevarakonda/maplet/internal/graph"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage:")
		fmt.Println("	maplet analyze <path>")
		fmt.Println("	maplet export <path>")
		fmt.Println("	maplet serve <path>")
		fmt.Println("	maplet mcp <path>")
		fmt.Println("	maplet nav <path>")
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

	case "mcp":
		result := analyzer.Analyze(path)
		mcpSrv := server.NewMCPServer(result)
		stdioSrv := mcpserver.NewStdioServer(mcpSrv)
		if err := stdioSrv.Listen(context.Background(), os.Stdin, os.Stdout); err != nil {
			panic(err)
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
