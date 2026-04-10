package server

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/abhinavdevarakonda/maplet/internal/analyzer"
	"github.com/abhinavdevarakonda/maplet/internal/graph"
	"github.com/abhinavdevarakonda/maplet/internal/tracer"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func NewMCPServer(result *analyzer.Result) *mcpserver.MCPServer {
	s := mcpserver.NewMCPServer("maplet", "1.0.0")

	// global tool to change the current working project
	s.AddTool(mcp.NewTool("set_project_root",
		mcp.WithDescription("Change the target project directory for analysis"),
		mcp.WithString("path", mcp.Description("Absolute path to the project root"), mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		newPath, _ := request.RequireString("path")
		newResult := analyzer.Analyze(newPath)
		*result = newResult // update shared pointer

		var funcCount int
		for _, n := range result.Graph.Nodes {
			if n.Type == graph.FunctionNode {
				funcCount++
			}
		}

		return mcp.NewToolResultText(fmt.Sprintf("Successfully switched to %s. Found %d functions.", newPath, funcCount)), nil
	})

	s.AddTool(mcp.NewTool("find_symbol",
		mcp.WithDescription("Find function/symbol IDs by name"),
		mcp.WithString("name", mcp.Description("Symbol name"), mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, _ := request.RequireString("name")
		var matches []string
		for id, n := range result.Graph.Nodes {
			if n.Name == name && n.Type == graph.FunctionNode {
				matches = append(matches, fmt.Sprintf("%s (%s)", id, n.Path))
			}
		}
		if len(matches) == 0 {
			return mcp.NewToolResultText("No matching functions found."), nil
		}
		return mcp.NewToolResultText("Found functions:\n" + strings.Join(matches, "\n")), nil
	})

	s.AddTool(mcp.NewTool("get_node_details",
		mcp.WithDescription("Get detailed info about a node (dir, file, or function)"),
		mcp.WithString("id", mcp.Description("Node ID"), mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := request.RequireString("id")
		n, ok := result.Graph.Nodes[id]
		if !ok {
			return mcp.NewToolResultError("Node not found"), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("ID: %s\nType: %s\nPath: %s\nLine: %d", n.ID, n.Type, n.Path, n.Line)), nil
	})

	s.AddTool(mcp.NewTool("get_callers",
		mcp.WithDescription("Find immediate callers of a function"),
		mcp.WithString("function_id", mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := request.RequireString("function_id")
		callers := analyzer.ImpactAnalysis(result.Graph, id)
		var res []string
		for _, c := range callers {
			res = append(res, fmt.Sprintf("%s (at line %d)", c.ID, c.Line))
		}
		return mcp.NewToolResultText(strings.Join(res, "\n")), nil
	})

	s.AddTool(mcp.NewTool("get_callees",
		mcp.WithDescription("Find functions called by this function"),
		mcp.WithString("function_id", mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := request.RequireString("function_id")
		callees := analyzer.TraceAnalysis(result.Graph, id)
		var res []string
		for _, c := range callees {
			res = append(res, fmt.Sprintf("%s (at line %d)", c.ID, c.Line))
		}
		return mcp.NewToolResultText(strings.Join(res, "\n")), nil
	})

	s.AddTool(mcp.NewTool("impact_analysis",
		mcp.WithDescription("Transitively find all functions affected if this function changes"),
		mcp.WithString("function_id", mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := request.RequireString("function_id")
		affected := analyzer.TransitiveImpact(result.Graph, id)
		return mcp.NewToolResultText(strings.Join(affected, "\n")), nil
	})

	s.AddTool(mcp.NewTool("trace_calls",
		mcp.WithDescription("Transitively find all functions called by this function"),
		mcp.WithString("function_id", mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := request.RequireString("function_id")
		called := analyzer.TransitiveTrace(result.Graph, id)
		return mcp.NewToolResultText(strings.Join(called, "\n")), nil
	})

	s.AddTool(mcp.NewTool("call_path",
		mcp.WithDescription("Find a call path between two functions"),
		mcp.WithString("start_id", mcp.Required()),
		mcp.WithString("end_id", mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start, _ := request.RequireString("start_id")
		end, _ := request.RequireString("end_id")
		path := analyzer.FindPath(result.Graph, start, end)
		if len(path) == 0 {
			return mcp.NewToolResultText("No path found."), nil
		}
		return mcp.NewToolResultText(strings.Join(path, " -> ")), nil
	})

	s.AddTool(mcp.NewTool("get_file_symbols",
		mcp.WithDescription("List all functions defined in a specific file"),
		mcp.WithString("file_path", mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		path, _ := request.RequireString("file_path")
		var res []string
		for id, n := range result.Graph.Nodes {
			if n.Path == path && n.Type == graph.FunctionNode {
				res = append(res, fmt.Sprintf("%s (line %d)", id, n.Line))
			}
		}
		return mcp.NewToolResultText(strings.Join(res, "\n")), nil
	})

	s.AddTool(mcp.NewTool("get_node_source",
		mcp.WithDescription("Get the source code for a specific function node"),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, _ := request.RequireString("id")
		n, ok := result.Graph.Nodes[id]
		if !ok || n.Type != graph.FunctionNode {
			return mcp.NewToolResultError("Function node not found"), nil
		}

		// Use EndLine for precise extraction
		endLine := n.EndLine
		if endLine == 0 {
			endLine = n.Line + 20
		}

		f, err := os.Open(n.Path)
		if err != nil {
			return mcp.NewToolResultError("Could not open file"), nil
		}
		defer f.Close()

		var source []string
		scanner := bufio.NewScanner(f)
		curr := 1
		for scanner.Scan() {
			if curr >= n.Line && curr <= endLine {
				source = append(source, scanner.Text())
			}
			if curr > endLine {
				break
			}
			curr++
		}
		return mcp.NewToolResultText(strings.Join(source, "\n")), nil
	})

	s.AddTool(mcp.NewTool("run_trace",
		mcp.WithDescription("Run an arbitrary command and trace its function calls dynamically in the background"),
		mcp.WithString("command", mcp.Description("The shell command to trace, e.g. 'python app.py'"), mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		command, _ := request.RequireString("command")
		var traceLog []string

		err := tracer.RunLocal(command, func(e tracer.Event) {
			traceLog = append(traceLog, fmt.Sprintf("hit: %s at %s:%d", e.Name, filepath.Base(e.File), e.Line))
		})

		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to run command: %v", err)), nil
		}

		if len(traceLog) == 0 {
			return mcp.NewToolResultText("Command ran but no trace hits were collected."), nil
		}

		return mcp.NewToolResultText("Execution Trace Sequence:\n" + strings.Join(traceLog, "\n")), nil
	})

	return s
}
