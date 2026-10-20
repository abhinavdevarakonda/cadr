package server

import (
	"strings"
	"testing"

	"github.com/abhinavdevarakonda/cadr/internal/analyzer"
	"github.com/abhinavdevarakonda/cadr/internal/graph"
)

func impactGraph() *graph.Graph {
	g := graph.New()
	for _, n := range []*graph.Node{
		{ID: "main.main", Type: graph.FunctionNode, Name: "main", Path: "/app/main.go", Line: 1},
		{ID: "handlers.RegisterRoutes", Type: graph.FunctionNode, Name: "RegisterRoutes", Path: "/app/router.go", Line: 20},
		{ID: "handlers.ListBooks", Type: graph.FunctionNode, Name: "ListBooks", Path: "/app/handlers.go", Line: 42},
	} {
		g.AddNode(n)
	}
	g.AddEdge("handlers.RegisterRoutes", "handlers.ListBooks", graph.CallsEdge, 31)
	g.AddEdge("main.main", "handlers.ListBooks", graph.CallsEdge, 88)
	g.AddEdge("main.main", "handlers.RegisterRoutes", graph.CallsEdge, 14)
	return g
}

// The impact output must be an edit checklist: hop headings plus file:line per call
// site, not a bare list of names.
func TestFormatImpactIsAnEditList(t *testing.T) {
	hits := analyzer.CallersByHop(impactGraph(), "handlers.ListBooks")
	out := formatImpact("impact", "handlers.ListBooks", hits, false)

	for _, want := range []string{"hop 1", "hop 2", "3 call site(s)", "router.go:31", "main.go:88", "main.go:14", "-> handlers.ListBooks"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestFormatImpactNoCallSites(t *testing.T) {
	out := formatImpact("impact", "missing.Fn", nil, false)
	if !strings.Contains(out, "no call sites found") {
		t.Fatalf("unexpected output: %q", out)
	}
}
