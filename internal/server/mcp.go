package server

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/abhinavdevarakonda/cadr/internal/analyzer"
	"github.com/abhinavdevarakonda/cadr/internal/graph"
	"github.com/abhinavdevarakonda/cadr/internal/tracer"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// stillAnalyzing is the notice returned while the background analyzer is still
// filling the graph.
const stillAnalyzing = "cadr is still analyzing the project in the background. Please try again in a few seconds."

func NewMCPServer(results *ResultStore) *mcpserver.MCPServer {
	s := mcpserver.NewMCPServer("cadr", "1.0.0")

	// global tool to change the current working project
	s.AddTool(mcp.NewTool("set_project_root",
		mcp.WithDescription("Change the target project directory for analysis"),
		mcp.WithString("path", mcp.Description("Absolute path to the project root"), mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		newPath, _ := request.RequireString("path")
		newResult, err := analyzer.Analyze(newPath)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to analyze %s: %v", newPath, err)), nil
		}
		if newResult.Graph == nil {
			newResult.Graph = graph.New()
		}
		results.Set(&newResult)

		var funcCount int
		for _, n := range newResult.Graph.Nodes {
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
		res := results.Get()
		if res.Graph == nil {
			return mcp.NewToolResultText(stillAnalyzing), nil
		}
		name, _ := request.RequireString("name")
		var matches []string
		for id, n := range res.Graph.Nodes {
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
		res := results.Get()
		if res.Graph == nil {
			return mcp.NewToolResultText(stillAnalyzing), nil
		}
		id, _ := request.RequireString("id")
		n, ok := res.Graph.Nodes[id]
		if !ok {
			return mcp.NewToolResultError("Node not found"), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("ID: %s\nType: %s\nPath: %s\nLine: %d", n.ID, n.Type, n.Path, n.Line)), nil
	})

	s.AddTool(mcp.NewTool("get_callers",
		mcp.WithDescription("Immediate callers of a function and the line they call it from. Use it for a quick 'who uses this' check; use impact_analysis when you need the full transitive blast radius."),
		mcp.WithString("function_id", mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := results.Get()
		if res.Graph == nil {
			return mcp.NewToolResultText(stillAnalyzing), nil
		}
		id, _ := request.RequireString("function_id")
		callers := analyzer.ImpactAnalysis(res.Graph, id)
		var out []string
		for _, c := range callers {
			path := ""
			if n, ok := res.Graph.Nodes[c.ID]; ok {
				path = n.Path
			}
			out = append(out, fmt.Sprintf("%s  (%s:%d)", c.ID, shortFile(path), c.Line))
		}
		return mcp.NewToolResultText(strings.Join(out, "\n")), nil
	})

	s.AddTool(mcp.NewTool("get_callees",
		mcp.WithDescription("Immediate functions this one calls, with the line of each call. Use it to see direct dependencies; use trace_calls for the full transitive set."),
		mcp.WithString("function_id", mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := results.Get()
		if res.Graph == nil {
			return mcp.NewToolResultText(stillAnalyzing), nil
		}
		id, _ := request.RequireString("function_id")
		callees := analyzer.TraceAnalysis(res.Graph, id)
		var out []string
		for _, c := range callees {
			path := ""
			if n, ok := res.Graph.Nodes[c.ID]; ok {
				path = n.Path
			}
			out = append(out, fmt.Sprintf("%s  (%s:%d)", c.ID, shortFile(path), c.Line))
		}
		return mcp.NewToolResultText(strings.Join(out, "\n")), nil
	})

	s.AddTool(mcp.NewTool("impact_analysis",
		mcp.WithDescription("Everything that breaks if you change this function: the transitive callers, grouped by hop, each with the file:line where it calls the next hop down. Use it BEFORE editing a signature or behaviour — the result is an edit checklist, not a list of names. Follow up with get_node_source on the hop-1 call sites."),
		mcp.WithString("function_id", mcp.Required()),
		mcp.WithBoolean("full", mcp.Description("Do not truncate the call-site list")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := results.Get()
		if res.Graph == nil {
			return mcp.NewToolResultText(stillAnalyzing), nil
		}
		id, _ := request.RequireString("function_id")
		return mcp.NewToolResultText(formatImpact("impact", id, analyzer.CallersByHop(res.Graph, id), request.GetBool("full", false))), nil
	})

	s.AddTool(mcp.NewTool("trace_calls",
		mcp.WithDescription("Everything this function depends on, transitively, grouped by hop with the file:line of each call. Use it BEFORE changing an implementation to see what must keep working; use get_callees for the immediate callers/callees only."),
		mcp.WithString("function_id", mcp.Required()),
		mcp.WithBoolean("full", mcp.Description("Do not truncate the call-site list")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := results.Get()
		if res.Graph == nil {
			return mcp.NewToolResultText(stillAnalyzing), nil
		}
		id, _ := request.RequireString("function_id")
		return mcp.NewToolResultText(formatImpact("callees", id, analyzer.CalleesByHop(res.Graph, id), request.GetBool("full", false))), nil
	})

	s.AddTool(mcp.NewTool("call_path",
		mcp.WithDescription("Find a call path between two functions"),
		mcp.WithString("start_id", mcp.Required()),
		mcp.WithString("end_id", mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := results.Get()
		if res.Graph == nil {
			return mcp.NewToolResultText(stillAnalyzing), nil
		}
		start, _ := request.RequireString("start_id")
		end, _ := request.RequireString("end_id")
		path := analyzer.FindPath(res.Graph, start, end)
		if len(path) == 0 {
			return mcp.NewToolResultText("No path found."), nil
		}
		return mcp.NewToolResultText(strings.Join(path, " -> ")), nil
	})

	s.AddTool(mcp.NewTool("get_file_symbols",
		mcp.WithDescription("List all functions defined in a specific file"),
		mcp.WithString("file_path", mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := results.Get()
		if res.Graph == nil {
			return mcp.NewToolResultText(stillAnalyzing), nil
		}
		path, _ := request.RequireString("file_path")
		var out []string
		for id, n := range res.Graph.Nodes {
			if n.Path == path && n.Type == graph.FunctionNode {
				out = append(out, fmt.Sprintf("%s (line %d)", id, n.Line))
			}
		}
		return mcp.NewToolResultText(strings.Join(out, "\n")), nil
	})

	s.AddTool(mcp.NewTool("get_node_source",
		mcp.WithDescription("Get the source code for a specific function node"),
		mcp.WithString("id", mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := results.Get()
		if res.Graph == nil {
			return mcp.NewToolResultText(stillAnalyzing), nil
		}
		id, _ := request.RequireString("id")
		n, ok := res.Graph.Nodes[id]
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

	s.AddTool(mcp.NewTool("get_last_trace",
		mcp.WithDescription("Summarize the most recent recorded run. Equivalent to trace_summary(run=\"last\"), kept as a compatibility alias: it prints the run id, joins the static graph, and includes errors."),
		mcp.WithString("fn", mcp.Description("Optional: filter by function name")),
		mcp.WithBoolean("full", mcp.Description("Do not truncate the function list")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := results.Get()
		return summaryResult(res.Root, res.Graph, "last", request.GetString("fn", ""), request.GetBool("full", false))
	})

	s.AddTool(mcp.NewTool("trace_runs",
		mcp.WithDescription("List recorded trace runs (newest first)"),
		mcp.WithNumber("limit", mcp.Description("Max runs to list (default 20)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := results.Get()
		store := tracer.NewRunStore(res.Root)
		metas, err := store.List()
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(metas) == 0 {
			return mcp.NewToolResultText("No runs recorded yet. Use 'cadr rec \"<command>\"' to record one."), nil
		}
		limit := request.GetInt("limit", 20)
		var b strings.Builder
		fmt.Fprintf(&b, "%-34s %-19s %9s %4s %7s %4s  %s\n", "RUN", "STARTED", "DURATION", "EXIT", "EVENTS", "ERR", "CMD")
		for i, m := range metas {
			if i >= limit {
				fmt.Fprintf(&b, "... %d more runs (limit=N to raise)\n", len(metas)-limit)
				break
			}
			started := m.Started
			if t, err := time.Parse(time.RFC3339Nano, m.Started); err == nil {
				started = t.Local().Format("2006-01-02 15:04:05")
			}
			if started == "" {
				started = "(unknown)"
			}
			cmd := m.Cmd
			if cmd == "" {
				cmd = "(unknown — interrupted or pre-v3 run)"
			}
			dur := "-"
			if m.DurationMs > 0 {
				dur = durStr(m.DurationMs * int64(time.Millisecond))
			}
			fmt.Fprintf(&b, "%-34s %-19s %9s %4d %7d %4d  %s\n", m.ID, started, dur, m.ExitCode, m.EventCount, m.ErrorCount, cmd)
		}
		return mcp.NewToolResultText(b.String()), nil
	})

	s.AddTool(mcp.NewTool("trace_summary",
		mcp.WithDescription("Per-function summary of a recorded run (count, total, self time)"),
		mcp.WithString("run", mcp.Description("Run id or 'last' (default)")),
		mcp.WithString("fn", mcp.Description("Optional: restrict to a single function")),
		mcp.WithBoolean("full", mcp.Description("Do not truncate the function list")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := results.Get()
		return summaryResult(res.Root, res.Graph, request.GetString("run", "last"), request.GetString("fn", ""), request.GetBool("full", false))
	})

	s.AddTool(mcp.NewTool("trace_tree",
		mcp.WithDescription("Indented call tree for a recorded run. Scope it with ctx=<id> (one request), fn=<name> (one function's subtree) or route=<label> (that route's requests merged into one tree with call counts). Unscoped output on a busy run tells you to pick a scope instead of dumping every row."),
		mcp.WithString("run", mcp.Description("Run id or 'last' (default)")),
		mcp.WithString("ctx", mcp.Description("Optional: only this execution context (one request/goroutine); see trace_context for ids")),
		mcp.WithString("fn", mcp.Description("Optional: only this function's subtree, across the run")),
		mcp.WithString("route", mcp.Description("Optional: merge every request to this route into one tree with per-edge counts")),
		mcp.WithNumber("depth", mcp.Description("Max tree depth")),
		mcp.WithNumber("rows", mcp.Description("Max rows (default 40)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := results.Get()
		run, err := loadRun(res.Root, request.GetString("run", "last"))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		forest := tracer.BuildForest(run.Events)
		depth := -1
		if v := request.GetFloat("depth", 0); v > 0 {
			depth = int(v)
		}
		rows := request.GetInt("rows", 40)
		scope := treeScope{
			Ctx:   request.GetString("ctx", ""),
			Fn:    request.GetString("fn", ""),
			Route: request.GetString("route", ""),
		}
		out := treeOut{traceEnvelope: newEnvelope(run), Scope: scopeOf(scope)}
		return mcp.NewToolResultStructured(out, formatTree(run, forest, res.Root, scope, depth, rows)), nil
	})

	s.AddTool(mcp.NewTool("trace_diff",
		mcp.WithDescription("Compare two recorded runs: added/removed functions, count/time deltas and first divergence"),
		mcp.WithString("a", mcp.Description("Baseline run id or 'last'"), mcp.Required()),
		mcp.WithString("b", mcp.Description("Candidate run id"), mcp.Required()),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := results.Get()
		aID, _ := request.RequireString("a")
		bID, _ := request.RequireString("b")
		a, err := loadRun(res.Root, aID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		b, err := loadRun(res.Root, bID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if a.ID == b.ID {
			return mcp.NewToolResultError(fmt.Sprintf("cannot diff run %s against itself; pass two different run ids", a.ID)), nil
		}
		diff := tracer.Diff(a.Events, b.Events)
		out := diffOut{A: a.ID, B: b.ID, Diff: diff, ARuns: runFilePath(a), BRuns: runFilePath(b)}
		return mcp.NewToolResultStructured(out, formatDiffText(diff, a.ID, b.ID)), nil
	})

	s.AddTool(mcp.NewTool("trace_context",
		mcp.WithDescription("Request/context index for a recorded run: one line per execution context (goroutine/thread/task), hottest first, labelled with the HTTP route when the handler is known. Start here for a server run, then call trace_tree with ctx=<id> or route=<label>."),
		mcp.WithString("run", mcp.Description("Run id or 'last' (default)")),
		mcp.WithNumber("limit", mcp.Description("Max contexts to list (default 20, hottest first)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := results.Get()
		run, err := loadRun(res.Root, request.GetString("run", "last"))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		forest := tracer.BuildForest(run.Events)
		entries, idle := ctxEntries(forest, res.Root)
		limit := request.GetInt("limit", 20)

		out := contextOut{
			traceEnvelope: newEnvelope(run),
			IdleContexts:  idle,
			ContextsTotal: len(entries),
		}
		for i, e := range entries {
			if limit > 0 && i >= limit {
				out.ContextsOmitted = len(entries) - limit
				break
			}
			out.Contexts = append(out.Contexts, contextRow{Ctx: e.Ctx, Route: e.Route, Fn: e.Fn, DurNs: e.Dur, Spans: e.Spans})
		}
		return mcp.NewToolResultStructured(out, formatContexts(run, res.Root, entries, idle, limit)), nil
	})

	s.AddTool(mcp.NewTool("trace_verify",
		mcp.WithDescription("Check a recorded run's internal consistency (span pairing, timing, seq integrity). Returns ok plus errors/warnings; open spans are warnings for live servers."),
		mcp.WithString("run", mcp.Description("Run id or 'last' (default)")),
	), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := results.Get()
		run, err := loadRun(res.Root, request.GetString("run", "last"))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		rep := tracer.Verify(run.Events)
		if res.Graph != nil {
			rep = tracer.VerifyWithGraph(run.Events, staticFuncs(res.Graph), 10)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "run %s: ok=%v events=%d enter=%d exit=%d ctx=%d open=%d dropped=%d\n",
			run.ID, rep.OK, rep.Counts.Events, rep.Counts.Enters, rep.Counts.Exits, rep.Counts.Contexts, rep.Counts.OpenSpans, rep.Counts.Dropped)
		for _, v := range rep.Errors {
			fmt.Fprintf(&b, "ERROR %s: %s %s:%d\n", v.Check, v.Detail, v.File, v.Line)
		}
		for _, v := range rep.Warnings {
			fmt.Fprintf(&b, "warn %s: %s\n", v.Check, v.Detail)
		}
		b.WriteString(tracer.FormatErrors(run.Errors, res.Root))
		out := verifyOut{
			traceEnvelope: newEnvelope(run),
			OK:            rep.OK,
			Counts:        rep.Counts,
			Violations:    rep.Errors,
			Warnings:      rep.Warnings,
		}
		return mcp.NewToolResultStructured(out, b.String()), nil
	})

	return s
}

func loadRun(root, id string) (*tracer.TraceRun, error) {
	return tracer.NewRunStore(root).Load(id)
}

// staticFuncs adapts the in-memory graph to the tracer's static-join shape.
func staticFuncs(g *graph.Graph) []tracer.StaticFunc {
	var sf []tracer.StaticFunc
	for _, n := range g.Nodes {
		if n.Type == graph.FunctionNode {
			sf = append(sf, tracer.StaticFunc{Name: n.Name, File: n.Path, Line: n.Line, EndLine: n.EndLine})
		}
	}
	return sf
}

// summaryResult renders the per-run digest. trace_summary and get_last_trace (a
// compatibility alias) both go through it, so the two can never describe
// different runs.
func summaryResult(root string, g *graph.Graph, runID, fn string, full bool) (*mcp.CallToolResult, error) {
	run, err := loadRun(root, runID)
	if err != nil {
		if runID == "" || runID == "last" {
			return mcp.NewToolResultError("No recorded trace found. Record one with 'cadr rec \"<command>\"' first."), nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}
	if g != nil {
		run.Summary.AddGraphAnomalies(staticFuncs(g), 10)
		run.Errors = tracer.Localize(run.Errors, staticFuncs(g))
	}
	if fn != "" {
		s, ok := run.Summary.FnStatFor(fn)
		if !ok {
			return mcp.NewToolResultText(fmt.Sprintf("function %q not found in run %s", fn, run.ID)), nil
		}
		out := summaryOut{
			traceEnvelope: newEnvelope(run),
			Functions:     []tracer.FnStat{s},
			Contexts:      len(run.Summary.Contexts),
		}
		return mcp.NewToolResultStructured(out, formatFnStat(s)), nil
	}
	var b strings.Builder
	b.WriteString(formatRunHeader(run, len(run.Errors)))
	b.WriteString(tracer.FormatErrors(run.Errors, root))
	b.WriteString(formatSummaryText(run.Summary, full))
	b.WriteString("\nfull run: " + runFilePath(run) + "\n")

	warnings, anomalies := splitAnomalies(run.Summary)
	out := summaryOut{
		traceEnvelope: newEnvelope(run),
		Functions:     run.Summary.Functions,
		Contexts:      len(run.Summary.Contexts),
		Warnings:      warnings,
		Anomalies:     anomalies,
	}
	return mcp.NewToolResultStructured(out, b.String()), nil
}

func formatFnStat(s tracer.FnStat) string {
	return fmt.Sprintf("%s  count=%d total=%s self=%s p50=%s p95=%s max=%s\n  %s:%d\n",
		s.Fn, s.Count, durStr(s.Total), durStr(s.Self), durStr(s.P50), durStr(s.P95), durStr(s.Max), s.File, s.Line)
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
		if res.OrderChanged {
			b.WriteString("  order changed (same functions, different sequence)\n")
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
