package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/abhinavdevarakonda/maplet/internal/analyzer"
	"github.com/abhinavdevarakonda/maplet/internal/server"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage:")
		fmt.Println("	maplet analyze <path>")
		fmt.Println("	maplet export <path>")
		fmt.Println("	maplet serve <path>")
		fmt.Println("	maplet mcp <path>")
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

		fmt.Printf(
			"graph: %d nodes, %d edges\n",
			len(result.Graph.Nodes),
			len(result.Graph.Edges),
		)

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
		if err := srv.Start("localhost:6767"); err != nil {
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
