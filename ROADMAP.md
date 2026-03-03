# Maplet Implementation Roadmap

---

## 🔴 #1 — Fix the Graph Adjacency Model (prerequisite for everything)

### The Problem

Right now the `Graph` struct looks like this:

```go
type Graph struct {
    Nodes map[string]*Node // fast ✅ — look up any node by ID in O(1)
    Edges []*Edge          // slow ❌ — to find edges, you must scan ALL of them
}
```

`Edges` is just a flat list. It has no structure. If you want to answer "what functions call `foo`?", you have to loop through **every single edge** and check if `edge.To == "foo"`. 

With 1,000 functions and 5,000 call edges this is fine. With 50,000 edges it becomes noticeably slow — and more importantly, **every** analysis feature (impact, trace, orphans, deps) needs to do this repeatedly.

```
Current: "who calls foo?"
→ loop edges[0]... edges[1]... edges[2]... edges[4999]... found it at edge[4876] ✅ (O(E))

With index: "who calls foo?"
→ AdjIn["foo"] → [bar, baz, qux]  ✅  (O(1))
```

### The Fix

Add two index maps to `Graph`, one for **outgoing** edges (what does X call?) and one for **incoming** edges (who calls X?):

```go
type Graph struct {
    Nodes  map[string]*Node
    Edges  []*Edge
    AdjOut map[string][]string // AdjOut["foo"] = ["bar", "baz"]  → foo calls bar and baz
    AdjIn  map[string][]string // AdjIn["foo"]  = ["main", "init"] → foo is called by main and init
}
```

Then add a `BuildIndex()` method that populates them from the existing `Edges` slice, called once after the graph is fully constructed:

```go
func (g *Graph) BuildIndex() {
    g.AdjOut = make(map[string][]string)
    g.AdjIn  = make(map[string][]string)
    for _, e := range g.Edges {
        if e.Type == CallsEdge {
            g.AdjOut[e.From] = append(g.AdjOut[e.From], e.To)
            g.AdjIn[e.To]   = append(g.AdjIn[e.To], e.From)
        }
    }
}
```

Call `g.BuildIndex()` at the end of `analyzer.Build()`, and everything downstream becomes fast and clean.

> [!IMPORTANT]
> This is a **prerequisite** — every other feature below depends on `AdjOut` and `AdjIn` existing.

---

## To-Do List

### ☐ 1. Fix Graph Adjacency Model

**File**: `internal/graph/graph.go`

- [ ] Add `AdjOut map[string][]string` and `AdjIn map[string][]string` fields to the `Graph` struct
- [ ] Add `BuildIndex()` method that iterates `Edges` and populates both maps (only for `CallsEdge`, skip `ContainsEdge`)

**File**: `internal/analyzer/analyzer.go`

- [ ] Call `g.BuildIndex()` at the end of `Build()`, after all facts/edges are added

---

### ☐ 2. ImpactAnalysis — "what breaks if I change X?"

**File**: `internal/analyzer/impact.go` *(new file)*

**What it does**: Starting from a function ID, walks the **reverse** call graph (`AdjIn`) recursively to find every function that transitively depends on it. Returns the full set of affected nodes.

```
Example: you change "db.Query"
Impact → ["repo.GetUser", "service.Login", "handler.LoginHandler", ...]
```

**Implementation sketch**:
```go
func ImpactAnalysis(g *graph.Graph, startID string) []string {
    visited := map[string]bool{}
    queue   := []string{startID}
    for len(queue) > 0 {
        id := queue[0]; queue = queue[1:]
        for _, caller := range g.AdjIn[id] {
            if !visited[caller] {
                visited[caller] = true
                queue = append(queue, caller)
            }
        }
    }
    // return visited keys as sorted slice
}
```

- [ ] Create `internal/analyzer/impact.go`
- [ ] Implement `ImpactAnalysis(g *graph.Graph, startID string) []string`

---

### ☐ 3. TraceExecution — "what does X call, all the way down?"

**File**: `internal/analyzer/trace.go` *(new file)*

**What it does**: Starting from a function ID, walks the **forward** call graph (`AdjOut`) recursively to find every function that X calls, transitively. Returns the ordered call chain.

```
Example: trace "handler.LoginHandler"  
Trace → ["service.Login", "repo.GetUser", "db.Query", ...]
```

Same BFS pattern as ImpactAnalysis, just uses `AdjOut` instead of `AdjIn`.

- [ ] Create `internal/analyzer/trace.go`
- [ ] Implement `TraceExecution(g *graph.Graph, startID string) []string`

---

### ☐ 4. Orphan Detection — "what functions are never called?"

**File**: `internal/analyzer/orphans.go` *(new file)*

**What it does**: After `BuildIndex()`, any `FunctionNode` with `len(AdjIn[id]) == 0` is never called by anything in the codebase. These are orphans — dead code candidates, or entry points.

- [ ] Create `internal/analyzer/orphans.go`
- [ ] Implement `FindOrphans(g *graph.Graph) []string` — returns IDs of all `FunctionNode`s with empty `AdjIn`

> [!NOTE]
> Entry points like `main()` and exported functions called from external packages will show up as orphans — the output should note this. Consider adding an `ExportedOnly bool` option to filter.

---

### ☐ 5. Module Dependency Aggregation — "which files depend on which?"

**File**: `internal/analyzer/deps.go` *(new file)*

**What it does**: Groups function-level `CallsEdge`s by their parent file. If `a/foo.go::FuncA` calls `b/bar.go::FuncB`, that becomes a file-level edge `a/foo.go → b/bar.go`. Produces a condensed, file-level dependency graph.

```
Example output:
  handlers/login.go  →  services/auth.go
  services/auth.go   →  db/queries.go
  services/auth.go   →  utils/hash.go
```

- [ ] Create `internal/analyzer/deps.go`
- [ ] Implement `ModuleDeps(g *graph.Graph) map[string][]string` — returns a `file → []file` map of cross-file call dependencies
- [ ] Optionally add a directory-level variant that groups by `filepath.Dir()`

---

### ☐ 6. CLI — Expose New Commands

**File**: `cmd/maplet/main.go`

Add these new commands to the `switch` block. Each one calls `analyzer.Analyze()`, then calls the relevant new function and prints results.

| Command | What It Does |
|---|---|
| `maplet impact <path> <id>` | Print all functions transitively affected by changing `<id>` |
| `maplet trace <path> <id>` | Print the full call chain from `<id>` downward |
| `maplet orphans <path>` | List all functions never called within the codebase |
| `maplet deps <path>` | Print file-to-file dependency summary |

- [ ] Add `case "impact":` — parse ID from `os.Args[3]`, call `ImpactAnalysis`, print results
- [ ] Add `case "trace":` — parse ID from `os.Args[3]`, call `TraceExecution`, print results
- [ ] Add `case "orphans":` — call `FindOrphans`, print results
- [ ] Add `case "deps":` — call `ModuleDeps`, print results
- [ ] Update the usage text at the top

---

### ☐ 7. Improve TUI

**File**: `internal/tui/tui.go`

Right now `l` (`currentView = "calls"`) is a stub — it switches the label but shows the same list. The TUI needs real views.

- [ ] Wire the `l` key: for the currently selected **function** node, call `TraceExecution` and display the call tree as an indented list
- [ ] Wire an `i` key: for the currently selected function, call `ImpactAnalysis` and display all callers
- [ ] Add a status bar at the bottom showing the selected node's ID, type, and path
- [ ] Handle the case where the selected item is a file (not a function) — disable call-view gracefully
- [ ] (Optional) Add a scrollable pane layout using lipgloss columns if the terminal width allows

---

### ☐ 8. MCP — Add New Tools

**File**: `internal/server/mcp.go`

Add four new tools alongside the existing `list_nodes`, `get_callers`, `get_callees`, `get_node_details`.

| Tool Name | Input | What It Does |
|---|---|---|
| `impact_analysis` | `function_id` | Returns all transitive callers (full impact set) |
| `trace_execution` | `function_id` | Returns full transitive callee chain |
| `find_orphans` | *(none)* | Returns all never-called function IDs |
| `module_deps` | *(none)* | Returns file-to-file dependency map as JSON |

- [ ] Add `impact_analysis` tool — call `ImpactAnalysis`, return newline-separated list
- [ ] Add `trace_execution` tool — call `TraceExecution`, return newline-separated list
- [ ] Add `find_orphans` tool — call `FindOrphans`, return newline-separated list
- [ ] Add `module_deps` tool — call `ModuleDeps`, marshal to JSON, return as text

---

## Implementation Order

```
1 → 2 → 3 → 4 → 5  (sequential, each builds on graph index from step 1)
         ↓
         6  (CLI wires 2-5)
         7  (TUI uses 2-3)
         8  (MCP uses 2-5)
```
