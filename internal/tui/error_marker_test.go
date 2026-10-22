package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/abhinavdevarakonda/cadr/internal/graph"
	"github.com/abhinavdevarakonda/cadr/internal/tracer"
)

// projectModel builds a model whose project root is a temp dir, plus that root, so
// frames can be expressed as absolute paths inside the project.
func projectModel(t *testing.T, nodes ...*graph.Node) (*Model, string) {
	t.Helper()
	root := t.TempDir()
	g := graph.New()
	for _, n := range nodes {
		g.Nodes[n.ID] = n
	}
	m := NewModel(g, root)
	return &m, root
}

func fn(id, name, relPath string, line, end int) *graph.Node {
	return &graph.Node{ID: id, Type: graph.FunctionNode, Name: name, Path: relPath, Line: line, EndLine: end}
}

// The line inside a function marks that function, with a short path in the status
// bar.
func TestApplyTraceErrorsMarksFunction(t *testing.T) {
	m, root := projectModel(t, fn("app.crash", "crash", "app/main.go", 40, 60))
	m.applyTraceErrors([]tracer.TraceError{{
		Kind:    tracer.ErrPanic,
		Message: "panic: index out of range",
		Fn:      "app.crash",
		File:    filepath.Join(root, "app", "main.go"),
		Line:    42,
	}})

	if !m.errored["app.crash"] {
		t.Fatalf("crash not marked: %v", m.errored)
	}
	if !strings.Contains(m.errText, "index out of range") {
		t.Fatalf("errText = %q", m.errText)
	}
	if !strings.Contains(m.errText, "main.go:42") {
		t.Fatalf("errText = %q, want the failing location", m.errText)
	}
	if strings.Contains(m.errText, root) {
		t.Fatalf("errText leaked an absolute path: %q", m.errText)
	}
}

func TestApplyTraceErrorsEmpty(t *testing.T) {
	m := newTestModel(t)
	m.applyTraceErrors(nil)
	if len(m.errored) != 0 || m.errText != "" {
		t.Fatalf("errored=%v errText=%q, want empty", m.errored, m.errText)
	}
}

// A module-level line owns no function: the caller above it must not be marked.
func TestApplyTraceErrorsModuleLevelOwnsNothing(t *testing.T) {
	m, root := projectModel(t, fn("api.load_db", "load_db", "server/api.py", 10, 18))
	m.applyTraceErrors([]tracer.TraceError{{
		Kind:    tracer.ErrException,
		Message: "ImportError: no module named x",
		Fn:      "<module>",
		File:    filepath.Join(root, "server", "api.py"),
		Line:    20, // module level, after load_db's range
	}})

	if len(m.errored) != 0 {
		t.Fatalf("marked %v, want nothing for a module-level failure", m.errored)
	}
	if !strings.Contains(m.errText, "ImportError") {
		t.Fatalf("errText = %q, want the message", m.errText)
	}
}

// A traceback whose head is in site-packages still marks the innermost *project*
// function.
func TestApplyTraceErrorsSkipsLibraryFrames(t *testing.T) {
	m, root := projectModel(t, fn("read_db._read_lines", "_read_lines", "helpers/read_db.py", 4, 6))
	app := filepath.Join(root, "helpers", "read_db.py")
	m.applyTraceErrors([]tracer.TraceError{{
		Kind:    tracer.ErrException,
		Message: "NameError: name 'x' is not defined",
		Fn:      "load_app",
		File:    "/venv/lib/site-packages/uvicorn/config.py",
		Line:    427,
		Frames: []tracer.Frame{
			{Fn: "<module>", File: filepath.Join(root, "server", "app.py"), Line: 2},
			{Fn: "load_app", File: "/venv/lib/site-packages/uvicorn/config.py", Line: 427},
			{Fn: "load_books", File: app, Line: 9},
			{Fn: "_read_lines", File: app, Line: 5},
		},
	}})

	if len(m.errored) != 1 || !m.errored["read_db._read_lines"] {
		t.Fatalf("errored = %v, want only read_db._read_lines", m.errored)
	}
	if !strings.Contains(m.errText, "read_db.py:5") {
		t.Fatalf("errText = %q", m.errText)
	}
}

// While a traceback is still arriving its innermost frame is a caller; an incomplete
// record must not be marked at all.
func TestApplyTraceErrorsIgnoresIncompleteRecord(t *testing.T) {
	m, root := projectModel(t,
		fn("read_db._read_lines", "_read_lines", "helpers/read_db.py", 4, 6),
		fn("read_db.load_books", "load_books", "helpers/read_db.py", 8, 10),
	)
	db := filepath.Join(root, "helpers", "read_db.py")

	// header only, then a caller frame, neither carrying a message yet
	m.applyTraceErrors([]tracer.TraceError{{Kind: tracer.ErrException}})
	m.applyTraceErrors([]tracer.TraceError{{
		Kind: tracer.ErrException, Fn: "load_books", File: db, Line: 9,
	}})
	if len(m.errored) != 0 {
		t.Fatalf("an incomplete record marked %v", m.errored)
	}

	// the complete record
	m.applyTraceErrors([]tracer.TraceError{{
		Kind:    tracer.ErrException,
		Message: "NameError: name 'x' is not defined",
		Fn:      "_read_lines",
		File:    db,
		Line:    5,
	}})
	if len(m.errored) != 1 || !m.errored["read_db._read_lines"] {
		t.Fatalf("errored = %v, want only read_db._read_lines", m.errored)
	}
}

// Two independent crashes in one run both stay marked.
func TestApplyTraceErrorsKeepsDistinctCrashes(t *testing.T) {
	m, root := projectModel(t,
		fn("app.a", "a", "app/a.go", 1, 10),
		fn("app.b", "b", "app/b.go", 1, 10),
	)
	m.applyTraceErrors([]tracer.TraceError{
		{Kind: tracer.ErrPanic, Message: "boom a", Fn: "app.a", File: filepath.Join(root, "app", "a.go"), Line: 4},
		{Kind: tracer.ErrPanic, Message: "boom b", Fn: "app.b", File: filepath.Join(root, "app", "b.go"), Line: 4},
	})
	if len(m.errored) != 2 || !m.errored["app.a"] || !m.errored["app.b"] {
		t.Fatalf("errored = %v, want both", m.errored)
	}
}

// Only the invocation that failed is red. A later invocation of the same function is
// not — this is the "same function still red after I fixed it" report.
func TestFlowRowMarksOnlyFailedInvocation(t *testing.T) {
	m := newTestModel(t) // nodes: fn.main, fn.child
	enter := func(name string, span, ts int64) tracer.Event {
		return tracer.Event{Name: name, Ev: tracer.EvEnter, Ctx: "1", Span: span, TS: ts}
	}
	exit := func(span, ts int64) tracer.Event {
		return tracer.Event{Ev: tracer.EvExit, Ctx: "1", Span: span, TS: ts}
	}
	// main(0) -> a(1) -> child(2), then main -> b(3) -> child(4)
	m.applyTraceEvent(enter("main", 1, 0))
	m.applyTraceEvent(enter("a", 2, 1))
	m.applyTraceEvent(enter("child", 3, 2))
	m.applyTraceEvent(exit(3, 3))
	m.applyTraceEvent(exit(2, 4))
	m.applyTraceEvent(enter("b", 4, 5))
	m.applyTraceEvent(enter("child", 5, 6))
	m.applyTraceEvent(exit(5, 7))
	m.applyTraceEvent(exit(4, 8))
	m.applyTraceEvent(exit(1, 9))

	m.erroredRows[2] = true // only the first child invocation failed

	marked, plain := 0, 0
	for _, g := range m.flowGroups() {
		line := m.renderFlowRow(g)
		if !strings.Contains(line, "child") {
			continue
		}
		if strings.Contains(line, "✗") {
			marked++
		} else {
			plain++
		}
	}
	if marked != 1 || plain != 1 {
		t.Fatalf("child rows: %d marked, %d plain (want exactly one of each)", marked, plain)
	}
}

// A collapsed repeat group is red when any invocation in it failed.
func TestFlowGroupWithFailureIsRed(t *testing.T) {
	m := newTestModel(t)
	m.applyTraceEvent(tracer.Event{Name: "child", File: "/p/main.go", Line: 5, Ev: tracer.EvEnter, Ctx: "1", Span: 1, TS: 0})
	m.applyTraceEvent(tracer.Event{Ev: tracer.EvExit, Ctx: "1", Span: 1, TS: 1})
	m.applyTraceEvent(tracer.Event{Name: "child", File: "/p/main.go", Line: 5, Ev: tracer.EvEnter, Ctx: "1", Span: 2, TS: 2})
	m.applyTraceEvent(tracer.Event{Ev: tracer.EvExit, Ctx: "1", Span: 2, TS: 3})

	groups := m.flowGroups()
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1 collapsed repeat group", len(groups))
	}
	m.erroredRows[1] = true // the second repeat failed
	if line := m.renderFlowRow(groups[0]); !strings.Contains(line, "✗") {
		t.Fatalf("group containing a failure is not marked: %q", line)
	}
}

// End to end: the failed row keeps its mark, a later successful invocation is clean,
// the tree un-reds on re-entry, and re-sent batches do not resurrect the mark.
func TestErrorMarkIsPerInvocation(t *testing.T) {
	m, root := projectModel(t, fn("read_db.load_borrows", "load_borrows", "helpers/read_db.py", 1, 10))
	db := filepath.Join(root, "helpers", "read_db.py")
	crash := tracer.TraceError{
		Kind:    tracer.ErrException,
		Message: "NameError: name 'x' is not defined",
		Fn:      "load_borrows",
		File:    db,
		Line:    3,
	}

	// first invocation, then the crash that names it
	m.applyTraceEvent(tracer.Event{Name: "load_borrows", File: db, Line: 2, Ev: tracer.EvEnter, Ctx: "1", Span: 1, TS: 0})
	m.applyTraceErrors([]tracer.TraceError{crash})
	if !m.erroredRows[0] || !m.errored["read_db.load_borrows"] {
		t.Fatalf("failed invocation not marked: rows=%v errored=%v", m.erroredRows, m.errored)
	}

	// the function runs again (the fix)
	m.applyTraceEvent(tracer.Event{Name: "load_borrows", File: db, Line: 2, Ev: tracer.EvEnter, Ctx: "1", Span: 2, TS: 10})
	if m.errored["read_db.load_borrows"] {
		t.Fatalf("tree still red after re-entry: %v", m.errored)
	}
	if !m.erroredRows[0] {
		t.Fatalf("the failed row lost its mark: %v", m.erroredRows)
	}
	if m.erroredRows[1] {
		t.Fatalf("the successful invocation is marked: %v", m.erroredRows)
	}

	// a later batch re-sending the same traceback must not resurrect it
	m.applyTraceErrors([]tracer.TraceError{crash})
	if m.erroredRows[1] || m.errored["read_db.load_borrows"] {
		t.Fatalf("old traceback resurrected the mark: rows=%v errored=%v", m.erroredRows, m.errored)
	}

	// a genuinely new crash marks the newer invocation
	m.applyTraceErrors([]tracer.TraceError{{
		Kind: tracer.ErrException, Message: "ValueError: something else",
		Fn: "load_borrows", File: db, Line: 3,
	}})
	if !m.erroredRows[1] || !m.errored["read_db.load_borrows"] {
		t.Fatalf("a new crash was not recorded: rows=%v errored=%v", m.erroredRows, m.errored)
	}
}

// A failed function that is selected must stay red instead of being repainted by
// the focus glow.
func TestFlowRowFocusKeepsErrorColour(t *testing.T) {
	m := newTestModel(t)
	m.applyTraceEvent(tracer.Event{Name: "child", File: "/p/main.go", Line: 5, Ev: tracer.EvEnter, Ctx: "1", Span: 2, TS: 10})
	m.erroredRows[0] = true
	m.playhead = 0 // focus the row
	groups := m.flowGroups()
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	if line := m.renderFlowRow(groups[0]); !strings.Contains(line, "✗") {
		t.Fatalf("focused row lost the error mark: %q", line)
	}
}

// The reported case: a module-level import chain with the parent in project code.
// Only the innermost project function may be marked.
func TestApplyTraceErrorsMarksChildNotParent(t *testing.T) {
	m, root := projectModel(t,
		fn("api.load_book_db", "load_book_db", "server/api_book_management.py", 16, 18),
		fn("read_db.load_books", "load_books", "helpers/read_db.py", 8, 12),
		fn("read_db._read_lines", "_read_lines", "helpers/read_db.py", 4, 6),
	)
	app := filepath.Join(root, "server", "app.py")
	api := filepath.Join(root, "server", "api_book_management.py")
	db := filepath.Join(root, "helpers", "read_db.py")

	m.applyTraceErrors([]tracer.TraceError{{
		Kind:    tracer.ErrException,
		Message: "NameError: name 'x' is not defined",
		Fn:      "<module>",
		File:    app,
		Line:    2,
		Frames: []tracer.Frame{
			{Fn: "<module>", File: app, Line: 2},
			{Fn: "<module>", File: api, Line: 20},
			{Fn: "load_book_db", File: api, Line: 17},
			{Fn: "load_books", File: db, Line: 12},
			{Fn: "_read_lines", File: db, Line: 4},
		},
	}})

	if len(m.errored) != 1 {
		t.Fatalf("errored = %v, want exactly one function", m.errored)
	}
	if !m.errored["read_db._read_lines"] {
		t.Fatalf("marked %v, want read_db._read_lines", m.errored)
	}
	if !strings.Contains(m.errText, "read_db.py:4") {
		t.Fatalf("errText = %q, want the failing location", m.errText)
	}
}
