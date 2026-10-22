package server

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abhinavdevarakonda/cadr/internal/analyzer"
	"github.com/abhinavdevarakonda/cadr/internal/frameworks"
	"github.com/abhinavdevarakonda/cadr/internal/tracer"
)

// formatRunHeader renders the identity of a run: which run, what was executed, how
// it ended and how much was traced. Every trace_* tool starts with it so the caller
// always knows what it is looking at.
func formatRunHeader(run *tracer.TraceRun, errCount int) string {
	wall := "0"
	if run.Meta.DurationMs > 0 {
		wall = durStr(run.Meta.DurationMs * int64(time.Millisecond))
	}
	traceDur := int64(0)
	events, enters, exits := 0, 0, 0
	if run.Summary != nil {
		traceDur = run.Summary.DurationNs
		events, enters, exits = run.Summary.EventCount, run.Summary.EnterCount, run.Summary.ExitCount
	}
	var b strings.Builder
	fmt.Fprintf(&b, "run %s\n", run.ID)
	fmt.Fprintf(&b, "  cmd=%q  exit_code=%d  complete=%v  errors=%d\n", run.Meta.Cmd, run.Meta.ExitCode, run.Meta.Complete, errCount)
	fmt.Fprintf(&b, "  wall=%s  trace=%s  events=%d (enter %d / exit %d)\n", wall, durStr(traceDur), events, enters, exits)
	return b.String()
}

// runFilePath is the raw JSONL escape hatch: the caller can read or grep the full
// run instead of asking cadr to inline it.
func runFilePath(run *tracer.TraceRun) string {
	return filepath.ToSlash(filepath.Join(".cadr", "traces", "runs", run.ID+".jsonl"))
}

// formatSummaryText renders the per-function digest: the hottest functions with
// their source location, then graph-join hints and trace anomalies as separate
// sections so a warning is not mistaken for a failure.
func formatSummaryText(sum *tracer.Summary, full bool) string {
	var b strings.Builder
	fns := append([]tracer.FnStat(nil), sum.Functions...)
	sort.Slice(fns, func(i, j int) bool { return fns[i].Total > fns[j].Total })
	limit := len(fns)
	if !full && limit > 25 {
		limit = 25
	}
	if len(fns) == 0 {
		b.WriteString("\nno function calls were recorded\n")
	} else {
		fmt.Fprintf(&b, "\n%-32s %6s %10s %10s  %s\n", "FUNCTION", "COUNT", "TOTAL", "SELF", "LOCATION")
		for _, f := range fns[:limit] {
			loc := ""
			if f.File != "" {
				loc = fmt.Sprintf("%s:%d", shortFile(f.File), f.Line)
			}
			fmt.Fprintf(&b, "%-32s %6d %10s %10s  %s\n", truncateName(f.Fn, 32), f.Count, durStr(f.Total), durStr(f.Self), loc)
		}
		if limit < len(fns) {
			fmt.Fprintf(&b, "... %d more functions (full:true to see all)\n", len(fns)-limit)
		}
	}
	warnings, anomalies := splitAnomalies(sum)
	if len(warnings) > 0 {
		b.WriteString("\nwarnings:\n")
		for _, a := range warnings {
			fmt.Fprintf(&b, "  %s: %s\n", a.Kind, a.Detail)
		}
	}
	if len(anomalies) > 0 {
		b.WriteString("\ntrace anomalies:\n")
		for _, a := range anomalies {
			fmt.Fprintf(&b, "  %s: %s\n", a.Kind, a.Detail)
		}
	}
	return b.String()
}

// splitAnomalies separates graph-join hints (coverage, not corruption) from trace
// integrity anomalies, so a caller can present warnings and failures apart.
func splitAnomalies(sum *tracer.Summary) (warnings, anomalies []tracer.Anomaly) {
	for _, a := range sum.Anomalies {
		if a.Kind == "dynamic_only" || a.Kind == "static_unused" {
			warnings = append(warnings, a)
			continue
		}
		anomalies = append(anomalies, a)
	}
	return warnings, anomalies
}

// shortFile keeps the last two path elements, which is enough to identify a file
// without printing the whole project prefix on every row.
func shortFile(p string) string {
	parts := strings.Split(filepath.ToSlash(p), "/")
	if len(parts) <= 2 {
		return p
	}
	return strings.Join(parts[len(parts)-2:], "/")
}

// ctxEntry is one context (one request/goroutine) summarized for the index view.
type ctxEntry struct {
	Ctx   string
	Route string
	Fn    string
	Dur   int64
	Spans int
}

// ctxEntries summarizes a forest's contexts, dropping synthetic idle contexts and
// labelling each one with its route when the handler can be matched.
func ctxEntries(forest *tracer.Forest, root string) (entries []ctxEntry, idle int) {
	routes := loadRouteTable(root)
	for _, c := range forest.Contexts {
		fn := ""
		var dur int64
		for _, r := range c.Roots {
			if r.Dur > dur {
				dur = r.Dur
				fn = r.Event.Name
			}
		}
		if fn == "" && dur == 0 {
			idle++
			continue
		}
		entries = append(entries, ctxEntry{Ctx: c.Ctx, Route: routes.label(fn), Fn: fn, Dur: dur, Spans: countContextNodes(c)})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Dur != entries[j].Dur {
			return entries[i].Dur > entries[j].Dur
		}
		return entries[i].Ctx < entries[j].Ctx
	})
	return entries, idle
}

// countContextNodes counts every invocation in a context.
func countContextNodes(c *tracer.ContextTree) int {
	n := 0
	var walk func(*tracer.TreeNode)
	walk = func(node *tracer.TreeNode) {
		n++
		for _, ch := range node.Children {
			walk(ch)
		}
	}
	for _, r := range c.Roots {
		walk(r)
	}
	return n
}

// treeScope selects what trace_tree renders.
type treeScope struct {
	Ctx   string
	Fn    string
	Route string
}

// formatTree renders one request, one function's subtree, one route's merged tree,
// or (only for small runs) everything.
func formatTree(run *tracer.TraceRun, forest *tracer.Forest, root string, scope treeScope, depthLimit, maxRows int) string {
	var b strings.Builder
	b.WriteString(formatRunHeader(run, len(run.Errors)))
	b.WriteString(tracer.FormatErrors(run.Errors, root))
	b.WriteString("\n")

	switch {
	case scope.Route != "":
		b.WriteString(formatRouteTree(forest, root, scope.Route, depthLimit, maxRows))
	case scope.Fn != "":
		b.WriteString(formatFnSubtree(forest, scope.Fn, depthLimit, maxRows))
	case scope.Ctx != "":
		b.WriteString(formatCtxTree(forest, scope.Ctx, depthLimit, maxRows))
	default:
		b.WriteString(formatAllContexts(forest, root, depthLimit, maxRows))
	}
	b.WriteString("\nfull run: " + runFilePath(run) + "\n")
	return b.String()
}

// renderState carries the row budget through a recursive render.
type renderState struct {
	rows      int
	maxRows   int
	truncated bool
}

func (st *renderState) canWrite() bool {
	if st.maxRows > 0 && st.rows >= st.maxRows {
		st.truncated = true
		return false
	}
	return true
}

// renderNode writes one invocation and its children as an indented tree row.
func renderNode(b *strings.Builder, n *tracer.TreeNode, depth, depthLimit int, st *renderState) {
	if depthLimit >= 0 && depth > depthLimit {
		return
	}
	if !st.canWrite() {
		return
	}
	indent := strings.Repeat("  ", depth)
	conn := ""
	if depth > 0 {
		conn = "├─ "
		if n.Parent != nil && len(n.Parent.Children) > 0 && n.Parent.Children[len(n.Parent.Children)-1] == n {
			conn = "└─ "
		}
	}
	dur := ""
	switch {
	case n.Open:
		dur = " [open]"
	case n.Dur > 0:
		dur = fmt.Sprintf(" [%s self %s]", durStr(n.Dur), durStr(n.Self))
	}
	fmt.Fprintf(b, "%s%s%s%s\n", indent, conn, n.Event.Name, dur)
	st.rows++
	for _, c := range n.Children {
		renderNode(b, c, depth+1, depthLimit, st)
	}
}

// countNodesDepth counts invocations reachable within depthLimit.
func countNodesDepth(nodes []*tracer.TreeNode, depth, depthLimit int) int {
	if depthLimit >= 0 && depth > depthLimit {
		return 0
	}
	n := 0
	for _, node := range nodes {
		n++
		n += countNodesDepth(node.Children, depth+1, depthLimit)
	}
	return n
}

// truncationNote reports what was shown and how to see more. It never quotes the
// run's total row count in place of the amount actually rendered.
func truncationNote(shown, total, maxRows int) string {
	if shown >= total {
		return ""
	}
	return fmt.Sprintf("showing %d of %d rows (rows=N to raise; ctx=<id>, fn=<name> or route=<label> to narrow)\n", shown, total)
}

func formatCtxTree(forest *tracer.Forest, ctxID string, depthLimit, maxRows int) string {
	for _, c := range forest.Contexts {
		if c.Ctx != ctxID {
			continue
		}
		root := ""
		var dur int64
		for _, r := range c.Roots {
			if r.Dur > dur {
				dur = r.Dur
				root = r.Event.Name
			}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "ctx=%s  root=%s  dur=%s  %d spans\n", c.Ctx, root, durStr(dur), countContextNodes(c))
		st := &renderState{maxRows: maxRows}
		for _, r := range c.Roots {
			renderNode(&b, r, 0, depthLimit, st)
		}
		b.WriteString(truncationNote(st.rows, countNodesDepth(c.Roots, 0, depthLimit), maxRows))
		return b.String()
	}
	return fmt.Sprintf("ctx %q not found in this run (run trace_tree without ctx to list them)\n", ctxID)
}

// formatAllContexts renders every context, but only for a small run: dumping
// thousands of interleaved rows is never an answer.
func formatAllContexts(forest *tracer.Forest, root string, depthLimit, maxRows int) string {
	entries, idle := ctxEntries(forest, root)
	if len(entries) > 3 {
		var b strings.Builder
		fmt.Fprintf(&b, "this run has %d contexts", len(entries))
		if idle > 0 {
			fmt.Fprintf(&b, " (+%d idle)", idle)
		}
		fmt.Fprintf(&b, "; a whole-run tree would be %d rows.\n", len(forest.Flat))
		b.WriteString("pick one with ctx=<id>:\n")
		limit := len(entries)
		if limit > 10 {
			limit = 10
		}
		for _, e := range entries[:limit] {
			route := e.Route
			if route == "" {
				route = "-"
			}
			fmt.Fprintf(&b, "  ctx=%-5s %-24s %-32s %8s %4d spans\n",
				e.Ctx, truncateName(route, 24), truncateName(e.Fn, 32), durStr(e.Dur), e.Spans)
		}
		if limit < len(entries) {
			fmt.Fprintf(&b, "  ... %d more contexts\n", len(entries)-limit)
		}
		return b.String()
	}
	var b strings.Builder
	st := &renderState{maxRows: maxRows}
	total := 0
	for _, c := range forest.Contexts {
		rootFn := ""
		var dur int64
		for _, r := range c.Roots {
			if r.Dur > dur {
				dur = r.Dur
				rootFn = r.Event.Name
			}
		}
		if rootFn == "" && dur == 0 && len(c.Roots) == 0 {
			continue
		}
		fmt.Fprintf(&b, "ctx=%s  root=%s  dur=%s  %d spans\n", c.Ctx, rootFn, durStr(dur), countContextNodes(c))
		for _, r := range c.Roots {
			renderNode(&b, r, 0, depthLimit, st)
		}
		total += countNodesDepth(c.Roots, 0, depthLimit)
		b.WriteString("\n")
	}
	b.WriteString(truncationNote(st.rows, total, maxRows))
	return b.String()
}

// formatFnSubtree renders every occurrence of one function and what ran under it.
func formatFnSubtree(forest *tracer.Forest, fn string, depthLimit, maxRows int) string {
	var b strings.Builder
	st := &renderState{maxRows: maxRows}
	occurrences, total := 0, 0
	for _, c := range forest.Contexts {
		var matches []*tracer.TreeNode
		var walk func(*tracer.TreeNode)
		walk = func(n *tracer.TreeNode) {
			if n.Event.Name == fn || strings.HasSuffix(n.Event.Name, "."+fn) {
				matches = append(matches, n)
			}
			for _, ch := range n.Children {
				walk(ch)
			}
		}
		for _, r := range c.Roots {
			walk(r)
		}
		for _, m := range matches {
			occurrences++
			fmt.Fprintf(&b, "[%d] ctx=%s\n", occurrences, c.Ctx)
			renderNode(&b, m, 0, depthLimit, st)
			b.WriteString("\n")
			total += countNodesDepth([]*tracer.TreeNode{m}, 0, depthLimit)
		}
	}
	if occurrences == 0 {
		return fmt.Sprintf("function %q did not run in this run (try trace_summary to see what did)\n", fn)
	}
	fmt.Fprintf(&b, "%d occurrence(s) of %s\n", occurrences, fn)
	b.WriteString(truncationNote(st.rows, total, maxRows))
	return b.String()
}

// routeTable maps a handler function to the HTTP routes that reach it.
type routesByHandler map[string][]string

// routeTable loads the routes a framework hook recorded and indexes them by
// handler so a context can be labelled with the request it served.
func loadRouteTable(root string) routesByHandler {
	eps, err := frameworks.LoadDynamicEndpoints(root)
	if err != nil {
		return nil
	}
	t := make(routesByHandler, len(eps))
	for _, ep := range eps {
		if ep.HandlerFunc == "" {
			continue
		}
		label := strings.TrimSpace(ep.Method + " " + ep.Path)
		t[ep.HandlerFunc] = append(t[ep.HandlerFunc], label)
	}
	for k := range t {
		sort.Strings(t[k])
	}
	return t
}

// label names the route(s) whose handler is fn, or "" when nothing matches.
func (t routesByHandler) label(fn string) string {
	if fn == "" {
		return ""
	}
	labels := t[fn]
	if len(labels) == 0 {
		short := fn
		if i := strings.LastIndex(fn, "."); i >= 0 {
			short = fn[i+1:]
		}
		labels = t[short]
	}
	if len(labels) == 0 {
		return ""
	}
	if len(labels) > 2 {
		return labels[0] + fmt.Sprintf(" +%d", len(labels)-1)
	}
	return strings.Join(labels, ", ")
}

// aggNode is the merged form of one function across many requests: a flame-graph
// node with a call count and worst-case duration.
type aggNode struct {
	Fn       string
	Count    int
	Total    int64
	Max      int64
	SelfMax  int64
	Children map[string]*aggNode
	Order    []string
}

func newAggNode(fn string) *aggNode {
	return &aggNode{Fn: fn, Children: make(map[string]*aggNode)}
}

// aggAdd folds one context's tree into the merged tree.
func aggAdd(dst *aggNode, treeNodes []*tracer.TreeNode) {
	for _, tn := range treeNodes {
		child, ok := dst.Children[tn.Event.Name]
		if !ok {
			child = newAggNode(tn.Event.Name)
			dst.Children[tn.Event.Name] = child
			dst.Order = append(dst.Order, tn.Event.Name)
		}
		child.Count++
		child.Total += tn.Dur
		if tn.Dur > child.Max {
			child.Max = tn.Dur
		}
		if tn.Self > child.SelfMax {
			child.SelfMax = tn.Self
		}
		aggAdd(child, tn.Children)
	}
}

// renderAgg writes the merged tree with per-edge call counts.
func renderAgg(b *strings.Builder, n *aggNode, depth, depthLimit int, st *renderState) {
	if depthLimit >= 0 && depth > depthLimit {
		return
	}
	if depth > 0 && !st.canWrite() {
		return
	}
	indent := strings.Repeat("  ", depth)
	conn := ""
	if depth > 0 {
		conn = "├─ "
	}
	avg := int64(0)
	if n.Count > 0 {
		avg = n.Total / int64(n.Count)
	}
	fmt.Fprintf(b, "%s%s%s [×%d avg %s max %s self %s]\n", indent, conn, n.Fn, n.Count, durStr(avg), durStr(n.Max), durStr(n.SelfMax))
	st.rows++
	for _, name := range n.Order {
		renderAgg(b, n.Children[name], depth+1, depthLimit, st)
	}
}

// formatRouteTree renders the merged call tree for one route: every request to it
// collapsed into a single tree with per-edge counts. This is the answer to "what
// does this endpoint actually do", and it stays small however many requests ran.
func formatRouteTree(forest *tracer.Forest, root, routeLabel string, depthLimit, maxRows int) string {
	entries, _ := ctxEntries(forest, root)
	matched := 0
	agg := newAggNode("")
	for _, e := range entries {
		if e.Route != routeLabel {
			continue
		}
		matched++
		for _, c := range forest.Contexts {
			if c.Ctx != e.Ctx {
				continue
			}
			aggAdd(agg, c.Roots)
		}
	}
	if matched == 0 {
		return fmt.Sprintf("no context matched route %q (run trace_tree without route to list the contexts and their labels)\n", routeLabel)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "route %s — %d request(s), merged call tree\n", routeLabel, matched)
	st := &renderState{maxRows: maxRows}
	for _, name := range agg.Order {
		renderAgg(&b, agg.Children[name], 0, depthLimit, st)
	}
	b.WriteString(truncationNote(st.rows, countAggNodes(agg)-1, maxRows))
	return b.String()
}

// countAggNodes counts every node in the merged tree.
func countAggNodes(n *aggNode) int {
	c := 1
	for _, ch := range n.Children {
		c += countAggNodes(ch)
	}
	return c
}

// formatImpact renders hop-grouped call sites. Each line is a file:line to visit, so
// the caller gets an edit checklist instead of a list of names it must chase.
func formatImpact(title, target string, hits []analyzer.ImpactHit, full bool) string {
	var b strings.Builder
	if len(hits) == 0 {
		fmt.Fprintf(&b, "%s: %s — no call sites found (is the function unreachable, or is the graph still building?)\n", title, target)
		return b.String()
	}
	hops := hits[len(hits)-1].Hop
	fmt.Fprintf(&b, "%s: %s — %d call site(s) in %d hop(s)\n", title, target, len(hits), hops)

	limit := len(hits)
	if !full && limit > 40 {
		limit = 40
	}
	shownHop := 0
	shownInHop := 0
	for i, h := range hits[:limit] {
		if h.Hop != shownHop {
			if shownHop != 0 {
				b.WriteString("\n")
			}
			shownHop = h.Hop
			shownInHop = 0
			for _, x := range hits {
				if x.Hop == shownHop {
					shownInHop++
				}
			}
			label := "callers — update these"
			if title == "callees" {
				label = "callees — what must keep working"
			}
			fmt.Fprintf(&b, "hop %d  (%d %s)\n", shownHop, shownInHop, label)
		}
		loc := h.File
		if loc == "" {
			loc = "(unknown)"
		}
		fmt.Fprintf(&b, "  %s:%d  %s -> %s\n", shortFile(loc), h.Line, truncateName(h.Fn, 32), truncateName(h.To, 32))
		_ = i
	}
	if limit < len(hits) {
		fmt.Fprintf(&b, "\n... %d more call site(s) (full:true to see all)\n", len(hits)-limit)
	}
	return b.String()
}
