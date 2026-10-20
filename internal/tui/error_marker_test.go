package tui

import (
	"strings"
	"testing"

	"github.com/abhinavdevarakonda/cadr/internal/graph"
	"github.com/abhinavdevarakonda/cadr/internal/tracer"
)

// A crash localized to a source line must mark exactly that function red and put
// the message in the status bar.
func TestApplyTraceErrorsMarksFunction(t *testing.T) {
	g := graph.New()
	g.Nodes["handlers.ListBooks"] = &graph.Node{
		ID: "handlers.ListBooks", Type: graph.FunctionNode, Name: "ListBooks",
		Path: "/app/handlers.go", Line: 40, EndLine: 60,
	}
	g.Nodes["handlers.Other"] = &graph.Node{
		ID: "handlers.Other", Type: graph.FunctionNode, Name: "Other",
		Path: "/app/handlers.go", Line: 100, EndLine: 120,
	}
	m := NewModel(g, t.TempDir())

	m.applyTraceErrors([]tracer.TraceError{{
		Kind:    tracer.ErrPanic,
		Message: "panic: runtime error: index out of range [7] with length 3",
		File:    "/app/handlers.go",
		Line:    42,
	}})

	if !m.errored["handlers.ListBooks"] {
		t.Fatalf("ListBooks not marked: %v", m.errored)
	}
	if m.errored["handlers.Other"] {
		t.Fatalf("unrelated function marked: %v", m.errored)
	}
	if !strings.Contains(m.errText, "panic: runtime error") {
		t.Fatalf("errText = %q", m.errText)
	}
	if !strings.Contains(m.errText, "/app/handlers.go:42") {
		t.Fatalf("errText = %q, want the failing location", m.errText)
	}
}

// An empty error list must not touch the model.
func TestApplyTraceErrorsEmpty(t *testing.T) {
	m := newTestModel(t)
	m.applyTraceErrors(nil)
	if len(m.errored) != 0 {
		t.Fatalf("errored = %v, want empty", m.errored)
	}
	if m.errText != "" {
		t.Fatalf("errText = %q, want empty", m.errText)
	}
}
