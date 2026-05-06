package analyzer

import (
	"sort"

	"github.com/abhinavdevarakonda/cadastre/internal/graph"
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
