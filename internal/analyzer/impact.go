package analyzer

import (
	"sort"

	"github.com/abhinavdevarakonda/cadr/internal/graph"
)

type ImpactResult struct {
	ID   string
	Line int
}

// ImpactAnalysis returns the immediate callers of startID with line numbers.
func ImpactAnalysis(g *graph.Graph, startID string) []ImpactResult {
	var results []ImpactResult
	for _, e := range g.Edges {
		if e.Type == graph.CallsEdge && e.To == startID {
			results = append(results, ImpactResult{
				ID:   e.From,
				Line: e.Line,
			})
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].ID != results[j].ID {
			return results[i].ID < results[j].ID
		}
		return results[i].Line < results[j].Line
	})
	return results
}

// TraceAnalysis returns the immediate callees of startID with line numbers.
func TraceAnalysis(g *graph.Graph, startID string) []ImpactResult {
	var results []ImpactResult
	for _, e := range g.Edges {
		if e.Type == graph.CallsEdge && e.From == startID {
			results = append(results, ImpactResult{
				ID:   e.To,
				Line: e.Line,
			})
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].ID != results[j].ID {
			return results[i].ID < results[j].ID
		}
		return results[i].Line < results[j].Line
	})
	return results
}

// TransitiveImpact returns all functions transitively affected by changing startID.
func TransitiveImpact(g *graph.Graph, startID string) []string {
	visited := make(map[string]bool)
	queue := []string{startID}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		for _, caller := range g.AdjIn[curr] {
			if !visited[caller] {
				visited[caller] = true
				queue = append(queue, caller)
			}
		}
	}

	var results []string
	for id := range visited {
		results = append(results, id)
	}
	sort.Strings(results)
	return results
}

// TransitiveTrace returns all functions transitively called by startID.
func TransitiveTrace(g *graph.Graph, startID string) []string {
	visited := make(map[string]bool)
	queue := []string{startID}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		for _, callee := range g.AdjOut[curr] {
			if !visited[callee] {
				visited[callee] = true
				queue = append(queue, callee)
			}
		}
	}

	var results []string
	for id := range visited {
		results = append(results, id)
	}
	sort.Strings(results)
	return results
}

// FindPath returns a sequence of function IDs representing a call path from start to end.
func FindPath(g *graph.Graph, start, end string) []string {
	parent := make(map[string]string)
	queue := []string{start}
	visited := map[string]bool{start: true}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		if curr == end {
			break
		}

		for _, next := range g.AdjOut[curr] {
			if !visited[next] {
				visited[next] = true
				parent[next] = curr
				queue = append(queue, next)
			}
		}
	}

	if _, ok := visited[end]; !ok {
		return nil
	}

	var path []string
	for curr := end; curr != ""; curr = parent[curr] {
		path = append([]string{curr}, path...)
		if curr == start {
			break
		}
	}
	return path
}

// ImpactHit is one call site reached from a target: who calls (or is called),
// where that call happens, and how many hops away it sits.
type ImpactHit struct {
	Fn   string `json:"fn"`   // the other end of the edge
	File string `json:"file"` // Fn's file
	Line int    `json:"line"` // line in Fn where the call happens
	To   string `json:"to"`   // the neighbour on the previous hop
	Hop  int    `json:"hop"`
}

// CallersByHop walks incoming call edges breadth-first and returns every affected
// caller with the source line where it invokes the next hop down. This is an edit
// checklist, unlike TransitiveImpact which returns bare ids.
func CallersByHop(g *graph.Graph, startID string) []ImpactHit {
	return walkByHop(g, startID, false)
}

// CalleesByHop is CallersByHop in the outward direction.
func CalleesByHop(g *graph.Graph, startID string) []ImpactHit {
	return walkByHop(g, startID, true)
}

func walkByHop(g *graph.Graph, startID string, outward bool) []ImpactHit {
	// Index call edges by the end we traverse from.
	byEndpoint := make(map[string][]*graph.Edge)
	for _, e := range g.Edges {
		if e.Type != graph.CallsEdge {
			continue
		}
		if outward {
			byEndpoint[e.From] = append(byEndpoint[e.From], e)
		} else {
			byEndpoint[e.To] = append(byEndpoint[e.To], e)
		}
	}

	// Each call *site* (edge) is reported once, while each node is expanded once so
	// a cycle cannot spin. That keeps both call sites when a function calls the
	// target directly and through a helper.
	visitedNode := map[string]bool{startID: true}
	seenEdge := make(map[[2]string]bool)
	type item struct {
		id  string
		hop int
	}
	queue := []item{{id: startID}}
	var hits []ImpactHit
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range byEndpoint[cur.id] {
			next := e.To
			if !outward {
				next = e.From
			}
			file := ""
			if n, ok := g.Nodes[next]; ok {
				file = n.Path
			}
			key := [2]string{e.From, e.To}
			if !seenEdge[key] {
				seenEdge[key] = true
				hits = append(hits, ImpactHit{Fn: next, File: file, Line: e.Line, To: cur.id, Hop: cur.hop + 1})
			}
			if !visitedNode[next] {
				visitedNode[next] = true
				queue = append(queue, item{id: next, hop: cur.hop + 1})
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Hop != hits[j].Hop {
			return hits[i].Hop < hits[j].Hop
		}
		if hits[i].Fn != hits[j].Fn {
			return hits[i].Fn < hits[j].Fn
		}
		return hits[i].Line < hits[j].Line
	})
	return hits
}
