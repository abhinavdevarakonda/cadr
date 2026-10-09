package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/abhinavdevarakonda/cadr/internal/analyzer"
	"github.com/abhinavdevarakonda/cadr/internal/graph"
	"github.com/abhinavdevarakonda/cadr/internal/tracer"
)

// traceFlags is the shared flag surface for `cadr trace <sub>`.
type traceFlags struct {
	run    string
	runSet bool
	json   bool
	full   bool
	all    bool
	strict bool
	graph  bool
	keep   int
	depth  int
	window int
	fn     string
	ctx    string
	pos    []string
}

func parseTraceFlags(args []string) traceFlags {
	f := traceFlags{run: "last", depth: -1, window: 20}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch {
		case a == "--json":
			f.json = true
		case a == "--full":
			f.full = true
		case a == "--all":
			f.all = true
		case a == "--strict":
			f.strict = true
		case a == "--graph":
			f.graph = true
		case a == "--run":
			f.run = next()
			f.runSet = true
		case strings.HasPrefix(a, "--run="):
			f.run = strings.TrimPrefix(a, "--run=")
			f.runSet = true
		case a == "--keep":
			if n, err := strconv.Atoi(next()); err == nil {
				f.keep = n
			}
		case strings.HasPrefix(a, "--keep="):
			if n, err := strconv.Atoi(strings.TrimPrefix(a, "--keep=")); err == nil {
				f.keep = n
			}
		case a == "--fn":
			f.fn = next()
		case strings.HasPrefix(a, "--fn="):
			f.fn = strings.TrimPrefix(a, "--fn=")
		case a == "--ctx":
			f.ctx = next()
		case strings.HasPrefix(a, "--ctx="):
			f.ctx = strings.TrimPrefix(a, "--ctx=")
		case a == "--depth":
			if n, err := strconv.Atoi(next()); err == nil {
				f.depth = n
			}
		case strings.HasPrefix(a, "--depth="):
			if n, err := strconv.Atoi(strings.TrimPrefix(a, "--depth=")); err == nil {
				f.depth = n
			}
		case a == "--window":
			if n, err := strconv.Atoi(next()); err == nil {
				f.window = n
			}
		case strings.HasPrefix(a, "--window="):
			if n, err := strconv.Atoi(strings.TrimPrefix(a, "--window=")); err == nil {
				f.window = n
			}
		default:
			if strings.HasPrefix(a, "-") {
				continue
			}
			f.pos = append(f.pos, a)
		}
	}
	return f
}

func traceUsage() {
	fmt.Println("usage: cadr trace <list|summary|tree|diff|context|verify|clear> [flags]")
	fmt.Println("  --run <id|last>   select a run (default: last)")
	fmt.Println("  --json            machine-readable output (see docs/trace-cli.md)")
	fmt.Println("  --full            do not truncate lists")
	fmt.Println("  --fn <name>       filter summary by function")
	fmt.Println("  --ctx <id>        restrict/select an execution context")
	fmt.Println("  --depth <n>       tree depth limit")
	fmt.Println("  --window <n>      rows shown before truncating")
	fmt.Println("  --strict          (verify) treat warnings as failures")
	fmt.Println("  --graph           (summary) join against the static call graph")
	fmt.Println("  --keep <n>        (clear) keep the newest n runs")
	fmt.Println("  --all             (clear) delete every run (default)")
	fmt.Println("  --yes             (clear) skip the confirmation prompt")
}

func runTraceCmd(args []string) {
	if len(args) == 0 {
		traceUsage()
		return
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list", "ls":
		traceList(rest)
	case "summary", "sum":
		traceSummary(rest)
	case "tree":
		traceTree(rest)
	case "diff":
		traceDiff(rest)
	case "context", "ctx":
		traceContext(rest)
	case "verify":
		traceVerify(rest)
	case "clear", "rm":
		traceClear(rest)
	case "help", "-h", "--help":
		traceUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown trace subcommand: %s\n", sub)
		traceUsage()
	}
}

func traceList(args []string) {
	f := parseTraceFlags(args)
	store := tracer.NewRunStore(".")
	metas, err := store.List()
	if err != nil {
		fail(err)
	}
	if f.json {
		printJSON(metas)
		return
	}
	if len(metas) == 0 {
		fmt.Println("no runs recorded yet. Run `cadr rec \"<command>\"` first.")
		return
	}
	fmt.Printf("%-28s %-20s %9s %4s %7s  %s\n", "RUN", "STARTED", "DURATION", "EXIT", "EVENTS", "CMD")
	limit := len(metas)
	if !f.full && limit > f.window {
		limit = f.window
	}
	for _, m := range metas[:limit] {
		started := m.Started
		if t, err := time.Parse(time.RFC3339Nano, m.Started); err == nil {
			started = t.Local().Format("2006-01-02 15:04:05")
		}
		fmt.Printf("%-28s %-20s %9s %4d %7d  %s\n", m.ID, started, humanMs(m.DurationMs), m.ExitCode, m.EventCount, m.Cmd)
	}
	if limit < len(metas) {
		fmt.Printf("... %d more (use --full)\n", len(metas)-limit)
	}
}

// traceClear deletes recorded runs. With no target flag it deletes all runs;
// --run <id> deletes one, --keep N retains the newest N. It confirms first
// unless -y/--yes is set.
func traceClear(args []string) {
	f := parseTraceFlags(args)
	store := tracer.NewRunStore(".")
	metas, err := store.List()
	if err != nil {
		fail(err)
	}

	var ids []string
	desc := ""
	switch {
	case f.keep > 0:
		if f.keep >= len(metas) {
			fmt.Printf("nothing to clear: %d run(s), keeping newest %d\n", len(metas), f.keep)
			return
		}
		for _, m := range metas[f.keep:] {
			ids = append(ids, m.ID)
		}
		desc = fmt.Sprintf("%d run(s) (keeping newest %d)", len(ids), f.keep)
	case f.runSet && !f.all:
		resolved, err := store.ResolveID(f.run)
		if err != nil {
			fail(err)
		}
		ids = []string{resolved}
		desc = "run " + resolved
	default:
		for _, m := range metas {
			ids = append(ids, m.ID)
		}
		desc = fmt.Sprintf("all %d run(s)", len(ids))
	}

	if len(ids) == 0 {
		fmt.Println("no runs to clear")
		return
	}
	if !analyzer.Confirm("delete " + desc + "?") {
		fmt.Println("aborted")
		return
	}

	var removed int
	switch {
	case f.keep > 0:
		removed, err = store.Keep(f.keep)
	case f.runSet && !f.all:
		err = store.Clear(ids[0])
		removed = 1
	default:
		removed, err = store.ClearAll()
	}
	if err != nil {
		fail(err)
	}
	if f.json {
		printJSON(map[string]interface{}{"deleted": ids, "count": removed})
		return
	}
	fmt.Printf("cleared %d run(s)\n", removed)
}

// traceVerify runs the deterministic invariant checks over a run.
func traceVerify(args []string) {
	f := parseTraceFlags(args)
	run := loadRun(f.run)
	rep := tracer.Verify(run.Events)

	failed := !rep.OK || (f.strict && len(rep.Warnings) > 0)
	if f.json {
		printJSON(rep)
		if failed {
			os.Exit(1)
		}
		return
	}

	status := "PASS"
	if !rep.OK {
		status = "FAIL"
	}
	fmt.Printf("run %s: %s  events=%d enter=%d exit=%d ctx=%d open=%d dropped=%d\n",
		run.ID, status, rep.Counts.Events, rep.Counts.Enters, rep.Counts.Exits, rep.Counts.Contexts, rep.Counts.OpenSpans, rep.Counts.Dropped)
	for _, v := range rep.Errors {
		fmt.Printf("  ERROR %-26s %s\n", v.Check, violationDetail(v))
	}
	for _, v := range rep.Warnings {
		fmt.Printf("  warn  %-26s %s\n", v.Check, violationDetail(v))
	}
	if len(rep.Errors) >= 50 || len(rep.Warnings) >= 50 {
		fmt.Println("  ... (truncated)")
	}
	if failed {
		os.Exit(1)
	}
}

func violationDetail(v tracer.Violation) string {
	s := v.Detail
	if v.Fn != "" {
		s += " " + v.Fn
	}
	if v.File != "" {
		s += fmt.Sprintf(" %s:%d", v.File, v.Line)
	}
	return strings.TrimSpace(s)
}

func traceSummary(args []string) {
	f := parseTraceFlags(args)
	run := loadRun(f.run)
	if f.graph {
		run.Summary.AddGraphAnomalies(staticFuncs("."), 10)
	}
	if f.json {
		if f.fn != "" {
			if s, ok := run.Summary.FnStatFor(f.fn); ok {
				printJSON([]tracer.FnStat{s})
				return
			}
			printJSON([]tracer.FnStat{})
			return
		}
		printJSON(run.Summary)
		return
	}
	if f.fn != "" {
		s, ok := run.Summary.FnStatFor(f.fn)
		if !ok {
			fmt.Printf("function %q not found in run %s\n", f.fn, run.ID)
			return
		}
		printFnTable([]tracer.FnStat{s}, true)
		return
	}
	fmt.Printf("run %s  cmd=%q  events=%d enter=%d exit=%d duration=%s exit_code=%d complete=%v\n",
		run.ID, run.Meta.Cmd, run.Meta.EventCount, run.Meta.EnterCount, run.Meta.ExitCount,
		humanMs(run.Meta.DurationMs), run.Meta.ExitCode, run.Meta.Complete)
	fns := append([]tracer.FnStat(nil), run.Summary.Functions...)
	sort.Slice(fns, func(i, j int) bool { return fns[i].Total > fns[j].Total })
	limit := len(fns)
	if !f.full && limit > f.window {
		limit = f.window
	}
	printFnTable(fns[:limit], true)
	if limit < len(fns) {
		fmt.Printf("... %d more functions (use --full)\n", len(fns)-limit)
	}
	if len(run.Summary.Contexts) > 0 {
		fmt.Printf("\ncontexts: %d\n", len(run.Summary.Contexts))
		climit := len(run.Summary.Contexts)
		if !f.full && climit > 5 {
			climit = 5
		}
		for _, c := range run.Summary.Contexts[:climit] {
			fmt.Printf("  ctx=%-14s root=%-28s dur=%-10s spans=%d\n", c.Ctx, c.Root, humanNs(c.Duration), c.Children)
		}
	}
	for _, a := range run.Summary.Anomalies {
		fmt.Printf("anomaly: %s: %s\n", a.Kind, a.Detail)
	}
}

func printFnTable(fns []tracer.FnStat, withFile bool) {
	fmt.Printf("%-32s %6s %10s %10s %10s %10s\n", "FUNCTION", "COUNT", "TOTAL", "SELF", "P95", "MAX")
	for _, s := range fns {
		fmt.Printf("%-32s %6d %10s %10s %10s %10s\n", truncate(s.Fn, 32), s.Count,
			humanNs(s.Total), humanNs(s.Self), humanNs(s.P95), humanNs(s.Max))
		if withFile && s.File != "" {
			fmt.Printf("  %s:%d\n", s.File, s.Line)
		}
	}
}

func traceTree(args []string) {
	f := parseTraceFlags(args)
	run := loadRun(f.run)
	forest := tracer.BuildForest(run.Events)
	if f.ctx != "" {
		forest = filterForest(forest, f.ctx)
	}
	if f.json {
		printJSON(treeJSON(run.ID, forest))
		return
	}
	rows := forest.DisplayRows()
	if len(rows) == 0 {
		fmt.Println("no enter events in this run.")
		return
	}
	for _, row := range rows {
		if f.depth >= 0 && row.Node != nil && row.Node.Depth > f.depth {
			continue
		}
		indent := strings.Repeat("  ", row.Depth)
		connector := ""
		if row.Depth > 0 {
			if row.Last {
				connector = "└─ "
			} else {
				connector = "├─ "
			}
		}
		marker := ""
		if row.Root {
			marker = "▸ "
		}
		dur := ""
		if row.Dur > 0 {
			dur = " [" + humanNs(row.Dur) + " self " + humanNs(row.Self) + "]"
		}
		fmt.Printf("%s%s%s%s%s\n", indent, connector, marker, row.Event.Name, dur)
	}
}

func traceDiff(args []string) {
	f := parseTraceFlags(args)
	if len(f.pos) < 2 {
		fmt.Fprintln(os.Stderr, "usage: cadr trace diff <run-a> <run-b>")
		return
	}
	store := tracer.NewRunStore(".")
	a, err := store.Load(f.pos[0])
	if err != nil {
		fail(err)
	}
	b, err := store.Load(f.pos[1])
	if err != nil {
		fail(err)
	}
	res := tracer.Diff(a.Events, b.Events)
	res.A, res.B = a.ID, b.ID
	if f.json {
		printJSON(res)
		return
	}
	fmt.Printf("diff %s -> %s\n", a.ID, b.ID)
	if len(res.Added) > 0 {
		fmt.Println("added:")
		for _, n := range res.Added {
			fmt.Printf("  + %s\n", n)
		}
	}
	if len(res.Removed) > 0 {
		fmt.Println("removed:")
		for _, n := range res.Removed {
			fmt.Printf("  - %s\n", n)
		}
	}
	fmt.Printf("%-32s %6s %6s %10s %10s\n", "FUNCTION", "A", "B", "TOTAL Δ", "SELF Δ")
	shown := 0
	for _, d := range res.Functions {
		if d.CountA == d.CountB && d.TotalA == d.TotalB && d.SelfA == d.SelfB {
			continue
		}
		fmt.Printf("%-32s %6d %6d %10s %10s\n", truncate(d.Fn, 32), d.CountA, d.CountB,
			deltaNs(d.TotalB-d.TotalA), deltaNs(d.SelfB-d.SelfA))
		shown++
		if !f.full && shown >= f.window {
			fmt.Println("  ... (use --full)")
			break
		}
	}
	if res.FirstDivergence < 0 {
		fmt.Println("first divergence: none (enter sequences identical)")
	} else {
		fmt.Printf("first divergence: enter index %d\n", res.FirstDivergence)
		if res.FirstDivergeA != nil {
			fmt.Printf("  a: %s %s:%d\n", res.FirstDivergeA.Name, res.FirstDivergeA.File, res.FirstDivergeA.Line)
		}
		if res.FirstDivergeB != nil {
			fmt.Printf("  b: %s %s:%d\n", res.FirstDivergeB.Name, res.FirstDivergeB.File, res.FirstDivergeB.Line)
		}
		if res.OrderChanged {
			fmt.Println("  order changed (same functions, different sequence)")
		}
	}
}

func traceContext(args []string) {
	f := parseTraceFlags(args)
	run := loadRun(f.run)
	forest := tracer.BuildForest(run.Events)
	if f.json {
		if f.ctx != "" {
			printJSON(treeJSON(run.ID, filterForest(forest, f.ctx)))
			return
		}
		printJSON(contextsJSON(run.ID, forest))
		return
	}
	if f.ctx == "" {
		fmt.Printf("run %s: %d context(s)\n", run.ID, len(forest.Contexts))
		for _, c := range forest.Contexts {
			root := ""
			var dur int64
			for _, r := range c.Roots {
				if r.Dur > dur {
					dur = r.Dur
					root = r.Event.Name
				}
			}
			fmt.Printf("  ctx=%-16s root=%-28s dur=%s\n", c.Ctx, root, humanNs(dur))
		}
		return
	}
	for _, row := range forest.Flat {
		if row.Ctx != f.ctx {
			continue
		}
		if f.depth >= 0 && row.Depth > f.depth {
			continue
		}
		dur := ""
		if row.Node != nil && row.Node.Dur > 0 {
			dur = " [" + humanNs(row.Node.Dur) + "]"
		}
		fmt.Printf("%s%s%s%s\n", strings.Repeat("  ", row.Depth), row.Ctx, row.Event.Name, dur)
	}
}

// contextJSONOut is the machine shape for `cadr trace context --json`.
type contextJSONOut struct {
	Run      string         `json:"run"`
	Schema   int            `json:"schema"`
	Contexts []contextEntry `json:"contexts"`
}

type contextEntry struct {
	Ctx        string `json:"ctx"`
	Root       string `json:"root"`
	DurationNs int64  `json:"duration_ns"`
	Spans      int    `json:"spans"`
}

func contextsJSON(runID string, forest *tracer.Forest) contextJSONOut {
	out := contextJSONOut{Run: runID, Schema: tracer.SchemaVersion}
	for _, c := range forest.Contexts {
		root := ""
		var dur int64
		for _, r := range c.Roots {
			if r.Dur > dur {
				dur = r.Dur
				root = r.Event.Name
			}
		}
		out.Contexts = append(out.Contexts, contextEntry{Ctx: c.Ctx, Root: root, DurationNs: dur, Spans: countNodes(c)})
	}
	return out
}

func countNodes(c *tracer.ContextTree) int {
	n := 0
	var rec func(*tracer.TreeNode)
	rec = func(node *tracer.TreeNode) {
		n++
		for _, ch := range node.Children {
			rec(ch)
		}
	}
	for _, r := range c.Roots {
		rec(r)
	}
	return n
}

// treeJSON is the machine shape for `cadr trace tree --json`.
type treeJSONOut struct {
	Run    string         `json:"run"`
	Schema int            `json:"schema"`
	Roots  []treeJSONNode `json:"roots"`
}

type treeJSONNode struct {
	Fn       string         `json:"fn"`
	File     string         `json:"file,omitempty"`
	Line     int            `json:"line,omitempty"`
	Ctx      string         `json:"ctx,omitempty"`
	DurNs    int64          `json:"dur_ns"`
	SelfNs   int64          `json:"self_ns"`
	Open     bool           `json:"open"`
	Children []treeJSONNode `json:"children,omitempty"`
}

func treeJSON(runID string, forest *tracer.Forest) treeJSONOut {
	out := treeJSONOut{Run: runID, Schema: tracer.SchemaVersion}
	var conv func(n *tracer.TreeNode, ctx string) treeJSONNode
	conv = func(n *tracer.TreeNode, ctx string) treeJSONNode {
		node := treeJSONNode{
			Fn: n.Event.Name, File: n.Event.File, Line: n.Event.Line, Ctx: ctx,
			DurNs: n.Dur, SelfNs: n.Self, Open: n.Open,
		}
		for _, c := range n.Children {
			node.Children = append(node.Children, conv(c, ctx))
		}
		return node
	}
	for _, ct := range forest.Contexts {
		for _, r := range ct.Roots {
			out.Roots = append(out.Roots, conv(r, ct.Ctx))
		}
	}
	return out
}

func filterForest(forest *tracer.Forest, ctx string) *tracer.Forest {
	out := &tracer.Forest{}
	for _, c := range forest.Contexts {
		if c.Ctx == ctx {
			out.Contexts = append(out.Contexts, c)
		}
	}
	for _, row := range forest.Flat {
		if row.Ctx == ctx {
			out.Flat = append(out.Flat, row)
		}
	}
	return out
}

// staticFuncs lists the project's static functions for the dynamic/static join.
func staticFuncs(root string) []tracer.StaticFunc {
	res := analyzer.Analyze(root)
	if res.Graph == nil {
		return nil
	}
	var out []tracer.StaticFunc
	for _, n := range res.Graph.Nodes {
		if n.Type == graph.FunctionNode {
			out = append(out, tracer.StaticFunc{Name: n.Name, File: n.Path, Line: n.Line})
		}
	}
	return out
}

func loadRun(id string) *tracer.TraceRun {
	store := tracer.NewRunStore(".")
	run, err := store.Load(id)
	if err != nil {
		fail(err)
	}
	return run
}

func printJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "cadr trace: %v\n", err)
	os.Exit(1)
}

func humanMs(ms int64) string {
	if ms < 1000 {
		return strconv.FormatInt(ms, 10) + "ms"
	}
	return humanNs(ms * int64(time.Millisecond))
}

func humanNs(ns int64) string {
	d := time.Duration(ns)
	switch {
	case d < time.Microsecond:
		return strconv.FormatInt(ns, 10) + "ns"
	case d < time.Millisecond:
		return strconv.FormatFloat(float64(ns)/1e3, 'f', 1, 64) + "µs"
	case d < time.Second:
		return strconv.FormatFloat(float64(ns)/1e6, 'f', 2, 64) + "ms"
	default:
		return strconv.FormatFloat(float64(ns)/1e9, 'f', 2, 64) + "s"
	}
}

func deltaNs(ns int64) string {
	if ns >= 0 {
		return "+" + humanNs(ns)
	}
	return humanNs(ns)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
