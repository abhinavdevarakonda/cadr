package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abhinavdevarakonda/cadr/internal/analyzer"
	"github.com/abhinavdevarakonda/cadr/internal/graph"
	"github.com/abhinavdevarakonda/cadr/internal/tracer"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func NewMCPServer(result *analyzer.Result) *mcpserver.MCPServer {
	s := mcpserver.NewMCPServer("cadr", "1.0.0")

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
		if result.Graph == nil {
			return mcp.NewToolResultText("cadr is still analyzing the project in the background. Please try again in a few seconds."), nil
		}
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
		if result.Graph == nil {
			return mcp.NewToolResultText("cadr is still analyzing the project in the background. Please try again in a few seconds."), nil
		}
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
		if result.Graph == nil {
			return mcp.NewToolResultText("cadr is still analyzing the project in the background. Please try again in a few seconds."), nil
		}
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
		if result.Graph == nil {
			return mcp.NewToolResultText("cadr is still analyzing the project in the background. Please try again in a few seconds."), nil
		}
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
		if result.Graph == nil {
			return mcp.NewToolResultText("cadr is still analyzing the project in the background. Please try again in a few seconds."), nil
		}
		id, _ := request.RequireString("function_id")
		affected := analyzer.TransitiveImpact(result.Graph, id)
		return mcp.NewToolResultText(strings.Join(affected, "\n")), nil
	})

	s.AddTool(mcp.NewTool("trace_calls",
		mcp.WithDescription("Transitively find all functions called by this function"),
		mcp.WithString("function_id", mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if result.Graph == nil {
			return mcp.NewToolResultText("cadr is still analyzing the project in the background. Please try again in a few seconds."), nil
		}
		id, _ := request.RequireString("function_id")
		called := analyzer.TransitiveTrace(result.Graph, id)
		return mcp.NewToolResultText(strings.Join(called, "\n")), nil
	})

	s.AddTool(mcp.NewTool("call_path",
		mcp.WithDescription("Find a call path between two functions"),
		mcp.WithString("start_id", mcp.Required()),
		mcp.WithString("end_id", mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if result.Graph == nil {
			return mcp.NewToolResultText("cadr is still analyzing the project in the background. Please try again in a few seconds."), nil
		}
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
		if result.Graph == nil {
			return mcp.NewToolResultText("cadr is still analyzing the project in the background. Please try again in a few seconds."), nil
		}
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
		if result.Graph == nil {
			return mcp.NewToolResultText("cadr is still analyzing the project in the background. Please try again in a few seconds."), nil
		}
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
		mcp.WithDescription("Run an arbitrary command and trace its function calls dynamically. Returns the exit code, a bounded per-function summary, and the first error line when the command fails."),
		mcp.WithString("command", mcp.Description("The shell command to trace, e.g. 'python app.py'"), mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		command, _ := request.RequireString("command")
		var events []tracer.Event

		runErr := tracer.RunLocal(command, func(e tracer.Event) {
			events = append(events, e)
		})

		exitCode := 0
		if runErr != nil {
			if ee, ok := runErr.(*exec.ExitError); ok {
				exitCode = ee.ExitCode()
			} else {
				exitCode = 1
			}
		}
		if len(events) == 0 {
			if runErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("command failed (exit %d): %v", exitCode, runErr)), nil
			}
			return mcp.NewToolResultText("Command ran but no trace hits were collected."), nil
		}

		sum := tracer.Summarize(events)
		var b strings.Builder
		fmt.Fprintf(&b, "exit_code: %d\n", exitCode)
		if runErr != nil {
			fmt.Fprintf(&b, "error: %v\n", runErr)
		}
		b.WriteString(formatSummaryText(sum, false))
		return mcp.NewToolResultText(b.String()), nil
	})

	s.AddTool(mcp.NewTool("get_last_trace",
		mcp.WithDescription("Summarize the last recorded trace from .cadr/traces/last_run.jsonl (created by 'cadr rec'). Bounded by default; set full:true for the raw JSONL lines."),
		mcp.WithString("fn", mcp.Description("Optional: filter by function name")),
		mcp.WithBoolean("full", mcp.Description("Return raw JSONL lines instead of a summary")),
		mcp.WithNumber("limit", mcp.Description("Optional: max raw lines when full:true")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		tracePath := filepath.Join(result.Root, ".cadr", "traces", "last_run.jsonl")
		events, err := tracer.ReadEvents(tracePath)
		if err != nil {
			return mcp.NewToolResultError("No recorded trace found. Run 'cadr rec <command>' first."), nil
		}
		fnFilter := request.GetString("fn", "")
		if fnFilter != "" {
			var filtered []tracer.Event
			for _, e := range events {
				if e.Name == fnFilter {
					filtered = append(filtered, e)
				}
			}
			events = filtered
		}
		if len(events) == 0 {
			if fnFilter != "" {
				return mcp.NewToolResultText(fmt.Sprintf("No calls to '%s' found in the last trace.", fnFilter)), nil
			}
			return mcp.NewToolResultText("Trace file is empty."), nil
		}

		if request.GetBool("full", false) {
			limit := request.GetInt("limit", 200)
			var lines []string
			for i, e := range events {
				if i >= limit {
					lines = append(lines, fmt.Sprintf("... %d more lines", len(events)-limit))
					break
				}
				data, _ := json.Marshal(e)
				lines = append(lines, string(data))
			}
			return mcp.NewToolResultText(fmt.Sprintf("Last recorded trace (%d events):\n%s", len(events), strings.Join(lines, "\n"))), nil
		}

		return mcp.NewToolResultText(formatSummaryText(tracer.Summarize(events), false)), nil
	})

	s.AddTool(mcp.NewTool("trace_runs",
		mcp.WithDescription("List recorded trace runs (newest first)"),
		mcp.WithNumber("limit", mcp.Description("Max runs to list (default 20)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		store := tracer.NewRunStore(result.Root)
		metas, err := store.List()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(metas) == 0 {
			return mcp.NewToolResultText("No runs recorded yet. Use run_trace or 'cadr rec'."), nil
		}
		limit := request.GetInt("limit", 20)
		var b strings.Builder
		for i, m := range metas {
			if i >= limit {
				fmt.Fprintf(&b, "... %d more runs\n", len(metas)-limit)
				break
			}
			fmt.Fprintf(&b, "%s  %s  %d events  exit=%d  cmd=%q\n", m.ID, m.Started, m.EventCount, m.ExitCode, m.Cmd)
		}
		return mcp.NewToolResultText(b.String()), nil
	})

	s.AddTool(mcp.NewTool("trace_summary",
		mcp.WithDescription("Per-function summary of a recorded run (count, total, self time)"),
		mcp.WithString("run", mcp.Description("Run id or 'last' (default)")),
		mcp.WithString("fn", mcp.Description("Optional: restrict to a single function")),
		mcp.WithBoolean("full", mcp.Description("Do not truncate the function list")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		run, err := loadRun(result.Root, request.GetString("run", "last"))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if result.Graph != nil {
			var sf []tracer.StaticFunc
			for _, n := range result.Graph.Nodes {
				if n.Type == graph.FunctionNode {
					sf = append(sf, tracer.StaticFunc{Name: n.Name, File: n.Path, Line: n.Line})
				}
			}
			run.Summary.AddGraphAnomalies(sf, 10)
		}
		if fn := request.GetString("fn", ""); fn != "" {
			if s, ok := run.Summary.FnStatFor(fn); ok {
				return mcp.NewToolResultText(formatFnStat(s)), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("function %q not found in run %s", fn, run.ID)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("run %s cmd=%q\n%s", run.ID, run.Meta.Cmd, formatSummaryText(run.Summary, request.GetBool("full", false)))), nil
	})

	s.AddTool(mcp.NewTool("trace_tree",
		mcp.WithDescription("Chronological call tree (indented) for a recorded run"),
		mcp.WithString("run", mcp.Description("Run id or 'last' (default)")),
		mcp.WithString("ctx", mcp.Description("Optional: only this execution context")),
		mcp.WithNumber("depth", mcp.Description("Max tree depth")),
		mcp.WithNumber("rows", mcp.Description("Max rows (default 200)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		run, err := loadRun(result.Root, request.GetString("run", "last"))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		forest := tracer.BuildForest(run.Events)
		depth := -1
		if v := request.GetFloat("depth", 0); v > 0 {
			depth = int(v)
		}
		rows := request.GetInt("rows", 200)
		return mcp.NewToolResultText("run " + run.ID + "\n" + formatForestText(forest, request.GetString("ctx", ""), depth, rows)), nil
	})

	s.AddTool(mcp.NewTool("trace_diff",
		mcp.WithDescription("Compare two recorded runs: added/removed functions, count/time deltas and first divergence"),
		mcp.WithString("a", mcp.Description("Baseline run id or 'last'"), mcp.Required()),
		mcp.WithString("b", mcp.Description("Candidate run id"), mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		aID, _ := request.RequireString("a")
		bID, _ := request.RequireString("b")
		a, err := loadRun(result.Root, aID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		b, err := loadRun(result.Root, bID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatDiffText(tracer.Diff(a.Events, b.Events), a.ID, b.ID)), nil
	})

	s.AddTool(mcp.NewTool("trace_context",
		mcp.WithDescription("List execution contexts (goroutines/threads/tasks) of a recorded run"),
		mcp.WithString("run", mcp.Description("Run id or 'last' (default)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		run, err := loadRun(result.Root, request.GetString("run", "last"))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		forest := tracer.BuildForest(run.Events)
		var b strings.Builder
		fmt.Fprintf(&b, "run %s: %d context(s)\n", run.ID, len(forest.Contexts))
		for _, c := range forest.Contexts {
			root := ""
			var dur int64
			for _, r := range c.Roots {
				if r.Dur > dur {
					dur = r.Dur
					root = r.Event.Name
				}
			}
			fmt.Fprintf(&b, "  ctx=%s root=%s dur=%s\n", c.Ctx, root, durStr(dur))
		}
		return mcp.NewToolResultText(b.String()), nil
	})

	s.AddTool(mcp.NewTool("trace_verify",
		mcp.WithDescription("Check a recorded run's internal consistency (span pairing, timing, seq integrity). Returns ok plus errors/warnings; open spans are warnings for live servers."),
		mcp.WithString("run", mcp.Description("Run id or 'last' (default)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		run, err := loadRun(result.Root, request.GetString("run", "last"))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		rep := tracer.Verify(run.Events)
		var b strings.Builder
		fmt.Fprintf(&b, "run %s: ok=%v events=%d enter=%d exit=%d ctx=%d open=%d dropped=%d\n",
			run.ID, rep.OK, rep.Counts.Events, rep.Counts.Enters, rep.Counts.Exits, rep.Counts.Contexts, rep.Counts.OpenSpans, rep.Counts.Dropped)
		for _, v := range rep.Errors {
			fmt.Fprintf(&b, "ERROR %s: %s %s:%d\n", v.Check, v.Detail, v.File, v.Line)
		}
		for _, v := range rep.Warnings {
			fmt.Fprintf(&b, "warn %s: %s\n", v.Check, v.Detail)
		}
		return mcp.NewToolResultText(b.String()), nil
	})

	return s
}

func loadRun(root, id string) (*tracer.TraceRun, error) {
	return tracer.NewRunStore(root).Load(id)
}

func formatFnStat(s tracer.FnStat) string {
	return fmt.Sprintf("%s  count=%d total=%s self=%s p50=%s p95=%s max=%s\n  %s:%d\n",
		s.Fn, s.Count, durStr(s.Total), durStr(s.Self), durStr(s.P50), durStr(s.P95), durStr(s.Max), s.File, s.Line)
}

func formatSummaryText(sum *tracer.Summary, full bool) string {
	var b strings.Builder
	fns := append([]tracer.FnStat(nil), sum.Functions...)
	sort.Slice(fns, func(i, j int) bool { return fns[i].Total > fns[j].Total })
	limit := len(fns)
	if !full && limit > 25 {
		limit = 25
	}
	fmt.Fprintf(&b, "events=%d enter=%d exit=%d duration=%s\n", sum.EventCount, sum.EnterCount, sum.ExitCount, durStr(sum.DurationNs))
	fmt.Fprintf(&b, "%-32s %6s %10s %10s\n", "FUNCTION", "COUNT", "TOTAL", "SELF")
	for _, f := range fns[:limit] {
		fmt.Fprintf(&b, "%-32s %6d %10s %10s\n", truncateName(f.Fn, 32), f.Count, durStr(f.Total), durStr(f.Self))
	}
	if limit < len(fns) {
		fmt.Fprintf(&b, "... %d more functions (full:true to see all)\n", len(fns)-limit)
	}
	for _, a := range sum.Anomalies {
		fmt.Fprintf(&b, "anomaly: %s: %s\n", a.Kind, a.Detail)
	}
	return b.String()
}

func formatForestText(forest *tracer.Forest, ctxFilter string, depthLimit, maxRows int) string {
	var b strings.Builder
	n := 0
	for _, row := range forest.DisplayRows() {
		if ctxFilter != "" && row.Ctx != ctxFilter {
			continue
		}
		if depthLimit >= 0 && row.Node != nil && row.Node.Depth > depthLimit {
			continue
		}
		if maxRows > 0 && n >= maxRows {
			fmt.Fprintf(&b, "... truncated (%d rows)\n", len(forest.Flat))
			break
		}
		indent := strings.Repeat("  ", row.Depth)
		conn := ""
		if row.Depth > 0 {
			if row.Last {
				conn = "└─ "
			} else {
				conn = "├─ "
			}
		}
		marker := ""
		if row.Root {
			marker = "▸ "
		}
		dur := ""
		if row.Dur > 0 {
			dur = " [" + durStr(row.Dur) + "]"
		}
		fmt.Fprintf(&b, "%s%s%s%s%s\n", indent, conn, marker, row.Event.Name, dur)
		n++
	}
	return b.String()
}

func formatDiffText(res *tracer.DiffResult, aID, bID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "diff %s -> %s\n", aID, bID)
	for _, n := range res.Added {
		fmt.Fprintf(&b, "  + %s\n", n)
	}
	for _, n := range res.Removed {
		fmt.Fprintf(&b, "  - %s\n", n)
	}
	fmt.Fprintf(&b, "%-32s %6s %6s %10s %10s\n", "FUNCTION", "A", "B", "TOTAL Δ", "SELF Δ")
	shown := 0
	for _, d := range res.Functions {
		if d.CountA == d.CountB && d.TotalA == d.TotalB && d.SelfA == d.SelfB {
			continue
		}
		fmt.Fprintf(&b, "%-32s %6d %6d %10s %10s\n", truncateName(d.Fn, 32), d.CountA, d.CountB, signedDur(d.TotalB-d.TotalA), signedDur(d.SelfB-d.SelfA))
		shown++
		if shown >= 25 {
			fmt.Fprintf(&b, "... more deltas omitted\n")
			break
		}
	}
	if res.FirstDivergence < 0 {
		b.WriteString("first divergence: none (enter sequences identical)\n")
	} else {
		fmt.Fprintf(&b, "first divergence: enter index %d\n", res.FirstDivergence)
		if res.FirstDivergeA != nil {
			fmt.Fprintf(&b, "  a: %s %s:%d\n", res.FirstDivergeA.Name, res.FirstDivergeA.File, res.FirstDivergeA.Line)
		}
		if res.FirstDivergeB != nil {
			fmt.Fprintf(&b, "  b: %s %s:%d\n", res.FirstDivergeB.Name, res.FirstDivergeB.File, res.FirstDivergeB.Line)
		}
	}
	return b.String()
}

func durStr(ns int64) string {
	switch {
	case ns == 0:
		return "0"
	case ns < 1000:
		return fmt.Sprintf("%dns", ns)
	case ns < 1000000:
		return fmt.Sprintf("%.1fµs", float64(ns)/1e3)
	case ns < 1000000000:
		return fmt.Sprintf("%.2fms", float64(ns)/1e6)
	default:
		return fmt.Sprintf("%.2fs", float64(ns)/1e9)
	}
}

func signedDur(ns int64) string {
	if ns >= 0 {
		return "+" + durStr(ns)
	}
	return durStr(ns)
}

func truncateName(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
