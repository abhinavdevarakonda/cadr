package analyzer

import (
	"testing"

	"github.com/abhinavdevarakonda/cadr/internal/graph"
)

func impactTestGraph() *graph.Graph {
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

// Every call site is reported once, with the hop it was reached at, so a direct
// call and an indirect call from the same function both appear.
func TestCallersByHop(t *testing.T) {
	hits := CallersByHop(impactTestGraph(), "handlers.ListBooks")
	if len(hits) != 3 {
		t.Fatalf("hits = %d, want 3: %+v", len(hits), hits)
	}
	if hits[0].Hop != 1 || hits[1].Hop != 1 || hits[2].Hop != 2 {
		t.Fatalf("hops = %d,%d,%d want 1,1,2", hits[0].Hop, hits[1].Hop, hits[2].Hop)
	}
	// The hop-2 entry is main.main calling RegisterRoutes at router.go:14.
	last := hits[2]
	if last.Fn != "main.main" || last.To != "handlers.RegisterRoutes" || last.Line != 14 || last.File != "/app/main.go" {
		t.Fatalf("hop-2 hit = %+v", last)
	}
}

func TestCalleesByHop(t *testing.T) {
	// ListBooks is reached twice (directly, and through RegisterRoutes), and each
	// call site is reported: 2 edges at hop 1, 1 edge at hop 2.
	hits := CalleesByHop(impactTestGraph(), "main.main")
	if len(hits) != 3 {
		t.Fatalf("hits = %d, want 3: %+v", len(hits), hits)
	}
	if hits[0].Hop != 1 || hits[1].Hop != 1 || hits[2].Hop != 2 {
		t.Fatalf("hops = %d,%d,%d want 1,1,2", hits[0].Hop, hits[1].Hop, hits[2].Hop)
	}
}
