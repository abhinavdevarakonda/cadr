package tui

import (
	"strings"
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

// TestFlowGroupsCollapseSiblings locks the always-on repeat-collapse rule:
// consecutive identical siblings under one parent render as a single row.
func TestFlowGroupsCollapseSiblings(t *testing.T) {
	m := newTestModel(t)
	enter := func(fn string, span, ts int64) tracer.Event {
		return tracer.Event{Name: fn, Ev: tracer.EvEnter, Ctx: "1", Span: span, TS: ts}
	}
	m.applyTraceEvent(enter("root", 1, 0))
	m.applyTraceEvent(enter("worker", 2, 1))
	m.applyTraceEvent(tracer.Event{Ev: tracer.EvExit, Ctx: "1", Span: 2, TS: 2})
	m.applyTraceEvent(enter("worker", 3, 3))
	m.applyTraceEvent(tracer.Event{Ev: tracer.EvExit, Ctx: "1", Span: 3, TS: 4})
	m.applyTraceEvent(enter("worker", 4, 5))
	m.applyTraceEvent(tracer.Event{Ev: tracer.EvExit, Ctx: "1", Span: 4, TS: 6})

	groups := m.flowGroups()
	if len(groups) != 2 {
		t.Fatalf("groups = %+v, want root + one collapsed run", groups)
	}
	if groups[0].start != 0 || groups[0].end != 0 {
		t.Fatalf("root group = %+v", groups[0])
	}
	if groups[1].start != 1 || groups[1].end != 3 {
		t.Fatalf("worker run = %+v, want {1 3}", groups[1])
	}

	// A playhead anywhere inside the run maps to the collapsed row.
	if got := flowGroupIndex(groups, 2); got != 1 {
		t.Fatalf("flowGroupIndex(playhead=2) = %d, want 1", got)
	}

	m.playhead = 2
	line := m.renderFlowRow(groups[1])
	if !strings.Contains(line, "> #2 ") {
		t.Fatalf("focused collapsed row = %q", line)
	}
	if !strings.Contains(line, "×3") {
		t.Fatalf("collapsed row missing count: %q", line)
	}
}

// TestFlowStepSkipsCollapsedRun is the scroll fix: one keypress moves the
// playhead to the next/previous display row, so it crosses a collapsed run of
// repeated calls instead of crawling through every hidden history index.
func TestFlowStepSkipsCollapsedRun(t *testing.T) {
	m := newTestModel(t)
	enter := func(fn string, span, ts int64) tracer.Event {
		return tracer.Event{Name: fn, Ev: tracer.EvEnter, Ctx: "1", Span: span, TS: ts}
	}
	m.applyTraceEvent(enter("root", 1, 0))
	for i, span := range []int64{2, 3, 4} {
		ts := int64(1 + i*2)
		m.applyTraceEvent(enter("worker", span, ts))
		m.applyTraceEvent(tracer.Event{Ev: tracer.EvExit, Ctx: "1", Span: span, TS: ts + 1})
	}
	m.applyTraceEvent(enter("tail", 5, 20))

	groups := m.flowGroups()
	if len(groups) != 3 || groups[1].end-groups[1].start+1 != 3 {
		t.Fatalf("groups = %+v", groups)
	}

	m.playhead = 0
	m.flowStep(1)
	if m.playhead != 1 {
		t.Fatalf("first step: playhead = %d, want 1", m.playhead)
	}
	m.flowStep(1)
	if m.playhead != 4 {
		t.Fatalf("step over collapsed run: playhead = %d, want 4", m.playhead)
	}
	m.flowStep(-1)
	if m.playhead != 1 {
		t.Fatalf("step back over collapsed run: playhead = %d, want 1", m.playhead)
	}

	// Starting inside the run still steps out in one keypress.
	m.playhead = 2
	m.flowStep(1)
	if m.playhead != 4 {
		t.Fatalf("step from inside run: playhead = %d, want 4", m.playhead)
	}
}

// TestFlowGroupsKeepDistinct guards the contract that only same-parent repeats
// collapse: __read_lines under two different load_* parents must stay separate.
func TestFlowGroupsKeepDistinct(t *testing.T) {
	m := newTestModel(t)
	m.applyTraceEvent(tracer.Event{Name: "load_books", Ev: tracer.EvEnter, Ctx: "1", Span: 1, TS: 0})
	m.applyTraceEvent(tracer.Event{Name: "__read_lines", Ev: tracer.EvEnter, Ctx: "1", Span: 2, TS: 1})
	m.applyTraceEvent(tracer.Event{Ev: tracer.EvExit, Ctx: "1", Span: 2, TS: 2})
	m.applyTraceEvent(tracer.Event{Ev: tracer.EvExit, Ctx: "1", Span: 1, TS: 3})
	m.applyTraceEvent(tracer.Event{Name: "load_authors", Ev: tracer.EvEnter, Ctx: "1", Span: 3, TS: 4})
	m.applyTraceEvent(tracer.Event{Name: "__read_lines", Ev: tracer.EvEnter, Ctx: "1", Span: 4, TS: 5})

	if len(m.history) != 4 {
		t.Fatalf("history = %d rows, want 4", len(m.history))
	}
	if m.parentIdx[1] == m.parentIdx[3] {
		t.Fatalf("fixture is wrong: __read_lines rows share a parent")
	}
	if m.sameFlowRow(1, 3) {
		t.Fatal("look-alike calls under different parents must not collapse")
	}
	for _, g := range m.flowGroups() {
		if g.end != g.start {
			t.Fatalf("unexpected collapsed group %+v", g)
		}
	}
}

// Distinct contexts must stay visible even when the root function name repeats,
// so the ▸ context-switch marker is never hidden.
func TestFlowGroupsKeepContextSwitch(t *testing.T) {
	m := newTestModel(t)
	m.applyTraceEvent(tracer.Event{Name: "main", Ev: tracer.EvEnter, Ctx: "1", Span: 1, TS: 0})
	m.applyTraceEvent(tracer.Event{Name: "main", Ev: tracer.EvEnter, Ctx: "2", Span: 2, TS: 1})
	for _, g := range m.flowGroups() {
		if g.end != g.start {
			t.Fatalf("context roots collapsed across a switch: %+v", g)
		}
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
