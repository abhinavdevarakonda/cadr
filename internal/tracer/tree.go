package tracer

// TreeNode is one invocation (a matched enter/exit pair, or an open enter).
type TreeNode struct {
	Event    Event
	Parent   *TreeNode
	Children []*TreeNode
	Depth    int

	Start int64 // ns since process start
	End   int64
	Dur   int64 // End - Start (0 when open or legacy)
	Self  int64 // Dur - sum(children.Dur)
	Open  bool
}

// FlatRow is one enter event in chronological order with its indentation depth.
// Exits never appear as rows; they annotate the matching node instead.
type FlatRow struct {
	Event   Event
	Depth   int
	Ctx     string
	CtxRoot bool // first enter of a context (a request/goroutine root)
	Last    bool // last sibling in the tree (drives the └─ connector)
	Node    *TreeNode
}

// ContextTree is the call forest for a single execution context.
type ContextTree struct {
	Ctx   string
	Roots []*TreeNode
}

// Forest is the result of building call trees from an ordered event stream.
// Flat preserves original enter order for row-oriented consumers (the TUI).
type Forest struct {
	Contexts []*ContextTree
	Flat     []FlatRow
}

// RootCtx is the synthetic context used for legacy/flat streams that carry no
// ctx field.
const RootCtx = "0"

// BuildForest groups events by ctx and reconstructs per-context call trees.
// Enters push, exits pop by span and annotate duration/self-time. Streams with
// no ctx collapse into a single context; streams with no exit events at all
// (legacy v1, mode:light) degrade to a flat list at depth 0.
func BuildForest(events []Event) *Forest {
	f := &Forest{}
	ctxs := make(map[string]*ContextTree)
	var order []string
	stacks := make(map[string][]*TreeNode)
	childDur := make(map[*TreeNode]int64)
	seenCtx := make(map[string]bool)

	hasExit := false
	for _, raw := range events {
		if NormalizeEvent(raw).Ev == EvExit {
			hasExit = true
			break
		}
	}

	ensure := func(ctx string) *ContextTree {
		ct, ok := ctxs[ctx]
		if !ok {
			ct = &ContextTree{Ctx: ctx}
			ctxs[ctx] = ct
			order = append(order, ctx)
		}
		return ct
	}

	for _, raw := range events {
		e := NormalizeEvent(raw)
		ctx := e.Ctx
		if ctx == "" {
			ctx = RootCtx
		}
		ct := ensure(ctx)

		if e.Ev == EvExit {
			if !hasExit {
				continue
			}
			st := stacks[ctx]
			idx := -1
			for i := len(st) - 1; i >= 0; i-- {
				if st[i].Event.Span == e.Span {
					idx = i
					break
				}
			}
			if idx < 0 {
				continue // exit without a known enter (dropped enter)
			}
			node := st[idx]
			node.End = e.TS
			if e.TS >= node.Start {
				node.Dur = e.TS - node.Start
			}
			node.Open = false
			node.Self = node.Dur - childDur[node]
			if node.Self < 0 {
				node.Self = 0
			}
			// Close any unbalanced children left open above this span.
			for i := len(st) - 1; i > idx; i-- {
				st[i].Open = false
			}
			stacks[ctx] = st[:idx]
			if node.Parent != nil {
				childDur[node.Parent] += node.Dur
			}
			continue
		}

		depth := 0
		ctxRoot := false
		if hasExit {
			depth = len(stacks[ctx])
			ctxRoot = depth == 0
		} else {
			ctxRoot = !seenCtx[ctx]
			seenCtx[ctx] = true
		}

		node := &TreeNode{
			Event: e,
			Depth: depth,
			Start: e.TS,
			Open:  hasExit,
		}
		if hasExit {
			if depth == 0 {
				ct.Roots = append(ct.Roots, node)
			} else {
				parent := stacks[ctx][depth-1]
				node.Parent = parent
				parent.Children = append(parent.Children, node)
			}
			stacks[ctx] = append(stacks[ctx], node)
		} else {
			ct.Roots = append(ct.Roots, node)
		}
		f.Flat = append(f.Flat, FlatRow{
			Event:   e,
			Depth:   depth,
			Ctx:     ctx,
			CtxRoot: ctxRoot,
			Node:    node,
		})
	}

	for _, ctx := range order {
		f.Contexts = append(f.Contexts, ctxs[ctx])
	}

	// A row is the last sibling when the next row is shallower or starts a new
	// context (or it is the final row).
	for i := range f.Flat {
		last := i == len(f.Flat)-1
		if !last {
			next := f.Flat[i+1]
			last = next.Depth < f.Flat[i].Depth || next.Ctx != f.Flat[i].Ctx
		}
		f.Flat[i].Last = last
	}
	return f
}

// Walk visits every node depth-first.
func (f *Forest) Walk(fn func(*TreeNode)) {
	var rec func(*TreeNode)
	rec = func(n *TreeNode) {
		fn(n)
		for _, c := range n.Children {
			rec(c)
		}
	}
	for _, ct := range f.Contexts {
		for _, r := range ct.Roots {
			rec(r)
		}
	}
}

// OpenSpans returns every node whose exit never arrived.
func (f *Forest) OpenSpans() []*TreeNode {
	var out []*TreeNode
	f.Walk(func(n *TreeNode) {
		if n.Open {
			out = append(out, n)
		}
	})
	return out
}

// DisplayRow is a render-ready row: visual depth (reset to 0 at a context
// switch), a context-root flag and per-context last-sibling flag. It is used by
// the CLI and MCP text renderers so interleaved contexts stay readable.
type DisplayRow struct {
	Event Event
	Ctx   string
	Depth int
	Root  bool
	Last  bool
	Node  *TreeNode
	Dur   int64
	Self  int64
}

// DisplayRows returns render-ready rows grouped by context (contexts in first
// appearance order, rows chronological within each). Grouping keeps interleaved
// goroutines from corrupting the tree connectors; the agent-facing CLI/MCP tree
// uses this shape. The live TUI stays chronological.
func (f *Forest) DisplayRows() []DisplayRow {
	n := len(f.Flat)
	last := make([]bool, n)
	for i := range last {
		last[i] = true
	}
	lastAtDepth := make(map[string]map[int]int)
	for i, row := range f.Flat {
		m := lastAtDepth[row.Ctx]
		if m == nil {
			m = make(map[int]int)
			lastAtDepth[row.Ctx] = m
		}
		if prev, ok := m[row.Depth]; ok {
			last[prev] = false
		}
		for d := range m {
			if d > row.Depth {
				delete(m, d)
			}
		}
		m[row.Depth] = i
	}

	var order []string
	byCtx := make(map[string][]int)
	for i, row := range f.Flat {
		if _, ok := byCtx[row.Ctx]; !ok {
			order = append(order, row.Ctx)
		}
		byCtx[row.Ctx] = append(byCtx[row.Ctx], i)
	}

	out := make([]DisplayRow, 0, n)
	for _, ctx := range order {
		for k, i := range byCtx[ctx] {
			row := f.Flat[i]
			dr := DisplayRow{Event: row.Event, Ctx: ctx, Depth: row.Depth, Root: k == 0, Last: last[i], Node: row.Node}
			if row.Node != nil {
				dr.Dur = row.Node.Dur
				dr.Self = row.Node.Self
			}
			out = append(out, dr)
		}
	}
	return out
}
