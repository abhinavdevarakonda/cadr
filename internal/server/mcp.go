package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/abhinavdevarakonda/maplet/internal/analyzer"
	"github.com/abhinavdevarakonda/maplet/internal/graph"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func NewMCPServer(result analyzer.Result) *mcpserver.MCPServer {
	s := mcpserver.NewMCPServer("maplet", "1.0.0")

	s.AddTool(mcp.NewTool("list_nodes",
		mcp.WithDescription("List all directories, files, and functions"),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var output []string
		for id, node := range result.Graph.Nodes {
			output = append(output, fmt.Sprintf("%s (%s)", id, node.Type))
		}
		return mcp.NewToolResultText(strings.Join(output, "\n")), nil
	})

	s.AddTool(mcp.NewTool("get_node_details",
		mcp.WithDescription("Get info about a node"),
		mcp.WithString("id", mcp.Description("Node ID"), mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := request.RequireString("id")
		n, ok := result.Graph.Nodes[id]
		if !ok {
			return mcp.NewToolResultError("not found"), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("ID: %s\nType: %s\nPath: %s", n.ID, n.Type, n.Path)), nil
	})

	s.AddTool(mcp.NewTool("get_callers",
		mcp.WithDescription("Find what calls this function"),
		mcp.WithString("function_id", mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := request.RequireString("function_id")
		var res []string
		for _, e := range result.Graph.Edges {
			if e.Type == graph.CallsEdge && e.To == id {
				res = append(res, e.From)
			}
		}
		return mcp.NewToolResultText(strings.Join(res, "\n")), nil
	})

	s.AddTool(mcp.NewTool("get_callees",
		mcp.WithDescription("Find what this function calls"),
		mcp.WithString("function_id", mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := request.RequireString("function_id")
		var res []string
		for _, e := range result.Graph.Edges {
			if e.Type == graph.CallsEdge && e.From == id {
				res = append(res, e.To)
			}
		}
		return mcp.NewToolResultText(strings.Join(res, "\n")), nil
	})

	return s
}
