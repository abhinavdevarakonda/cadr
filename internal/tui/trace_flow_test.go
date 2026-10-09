package tui

import (
	"testing"

	"github.com/abhinavdevarakonda/cadr/internal/graph"
	"github.com/abhinavdevarakonda/cadr/internal/tracer"
)

func newTestModel(t *testing.T) *Model {
	t.Helper()
	g := graph.New()
	g.Nodes["fn.main"] = &graph.Node{ID: "fn.main", Type: graph.FunctionNode, Name: "main", Path: "/p/main.go", Line: 1}
	g.Nodes["fn.child"] = &graph.Node{ID: "fn.child", Type: graph.FunctionNode, Name: "child", Path: "/p/main.go", Line: 5}
	m := NewModel(g, t.TempDir())
	return &m
}

// TestApplyTraceEventEnterOnlyRows is the guard for the v2 TUI contract: exits
// annotate, they never become rows, so history/hit counts keep v1 semantics.
func TestApplyTraceEventEnterOnlyRows(t *testing.T) {
	m := newTestModel(t)
	events := []tracer.Event{
		{Name: "main", File: "/p/main.go", Line: 1, Ev: tracer.EvEnter, Ctx: "1", Span: 1, TS: 0},
		{Name: "child", File: "/p/main.go", Line: 5, Ev: tracer.EvEnter, Ctx: "1", Span: 2, TS: 10},
		{Ev: tracer.EvExit, Ctx: "1", Span: 2, TS: 30},
		{Ev: tracer.EvExit, Ctx: "1", Span: 1, TS: 100},
	}
	for _, e := range events {
		m.applyTraceEvent(e)
	}
	if len(m.history) != 2 {
		t.Fatalf("history = %d rows, want 2 (enter only)", len(m.history))
	}
	if len(m.depths) != 2 || m.depths[0] != 0 || m.depths[1] != 1 {
		t.Fatalf("depths = %v, want [0 1]", m.depths)
	}
	if m.durNs[1] != 100 || m.selfNs[1] != 80 {
		t.Fatalf("main dur=%d self=%d, want 100/80", m.durNs[1], m.selfNs[1])
	}
	if m.durNs[2] != 20 || m.selfNs[2] != 20 {
		t.Fatalf("child dur=%d self=%d, want 20/20", m.durNs[2], m.selfNs[2])
	}
	// Heatmap counts enter events (once each) and is unaffected by exits.
	if m.hitCounts["fn.main"] != 1 || m.hitCounts["fn.child"] != 1 {
		t.Fatalf("hitCounts = %+v", m.hitCounts)
	}
	if !m.rowLast(0) || !m.rowLast(1) {
		t.Fatalf("rowLast: a root with no sibling and its only child are both last")
	}
}

// TestApplyTraceEventLastChild locks in the preorder last-child rule that drives
// the └─/├─ connectors.
func TestApplyTraceEventLastChild(t *testing.T) {
	m := newTestModel(t)
	e := func(fn string, span, ts int64) tracer.Event {
		return tracer.Event{Name: fn, Ev: tracer.EvEnter, Ctx: "1", Span: span, TS: ts}
	}
	m.applyTraceEvent(e("root", 1, 0))
	m.applyTraceEvent(e("a", 2, 1))
	m.applyTraceEvent(e("x", 3, 2))
	// Close the first subtree so root2 is a depth-0 sibling of root.
	m.applyTraceEvent(tracer.Event{Ev: tracer.EvExit, Ctx: "1", Span: 3, TS: 3})
	m.applyTraceEvent(tracer.Event{Ev: tracer.EvExit, Ctx: "1", Span: 2, TS: 4})
	m.applyTraceEvent(tracer.Event{Ev: tracer.EvExit, Ctx: "1", Span: 1, TS: 5})
	m.applyTraceEvent(e("root2", 4, 6))
	m.applyTraceEvent(e("b1", 5, 7))

	if m.rowLast(0) {
		t.Fatalf("root should not be last once root2 is a sibling")
	}
	if !m.rowLast(1) { // a: only child
		t.Fatalf("a should be last (only child)")
	}
	if !m.rowLast(2) { // x: subtree closed by root2
		t.Fatalf("x should be last once its subtree closed")
	}
	if !m.rowLast(4) { // b1: only child so far
		t.Fatalf("b1 should be last while it is the only child")
	}

	m.applyTraceEvent(tracer.Event{Ev: tracer.EvExit, Ctx: "1", Span: 5, TS: 8})
	m.applyTraceEvent(e("b2", 6, 9))
	if m.rowLast(4) {
		t.Fatalf("b1 should stop being last after sibling b2")
	}
	if !m.rowLast(5) {
		t.Fatalf("b2 should be last")
	}
}

func TestApplyTraceEventContextsAndLegacy(t *testing.T) {
	m := newTestModel(t)
	m.applyTraceEvent(tracer.Event{Name: "r1", Ev: tracer.EvEnter, Ctx: "1", Span: 1, TS: 0})
	m.applyTraceEvent(tracer.Event{Name: "r2", Ev: tracer.EvEnter, Ctx: "2", Span: 2, TS: 1})
	if len(m.depths) != 2 || m.depths[0] != 0 || m.depths[1] != 0 {
		t.Fatalf("second context must be a root: %v", m.depths)
	}
	if !m.ctxRoots[0] || !m.ctxRoots[1] {
		t.Fatalf("ctxRoots = %v", m.ctxRoots)
	}

	// Legacy v1 event (no ev/ctx/span) must still land as a row.
	before := len(m.history)
	m.applyTraceEvent(tracer.Event{Name: "legacy", File: "/p/main.go", Line: 9})
	if len(m.history) != before+1 {
		t.Fatalf("legacy event not appended")
	}
}
