# Contributing to cadr

Thank you for your interest in contributing to cadr. This guide explains how cadr is architected, how to set up your local development environment, and how to add new capabilities.

---

## Architecture Overview

cadr is built around three independent, cooperating subsystems:

```text
+-------------------------------------------------------------------+
|                     1. Static Analysis Engine                     |
| Source Files -> Tree-sitter AST -> Facts/Symbols -> Graph Index   |
| (.py, .go, .js, .c)   (.scm queries)   (Proximity)   (internal/graph) |
+---------------------------------+---------------------------------+
                                  |
                                  | Graph Nodes & Relations
                                  v
+-------------------------------------------------------------------+
|                     3. BubbleTea TUI Dashboard                    |
| Left Pane: Code Structure Tree (Static Graph)                     |
| Right Pane: Flow / Trace / Heatmaps (Dynamic Events)              |
+---------------------------------+---------------------------------+
                                  ^
                                  | 30 FPS Batched Events
+---------------------------------+---------------------------------+
|                     2. Dynamic Tracing Engine                     |
| Target Process -> Runtime Agent -> In-Memory Ring -> UDS/TCP IPC  |
| (cadr run/rec)    (sys.settrace /   (Event Buffer)   (/tmp/cadr.sock)|
|                    -toolexec /                                    |
|                    NODE_OPTIONS)                                  |
+-------------------------------------------------------------------+
```

1. **Static Analysis Engine (`internal/analyzer`, `internal/lang`, `queries`):** Scans source files using language-specific Tree-sitter grammar queries (`.scm`), extracts symbol definitions and outgoing function call sites, and builds a directed graph with adjacency indexes (`g.AdjIn`, `g.AdjOut`).
2. **Dynamic Tracing Engine (`internal/tracer`, `internal/agents`, `internal/toolwrap`):** Injects zero-dependency runtime probes into target applications, records function entries and exits with microsecond timestamps, and streams events across local IPC (Unix Domain Socket or TCP).
3. **Interactive TUI (`internal/tui`):** A terminal dashboard built on the Elm architecture (BubbleTea). Dynamically maps runtime execution events back onto the static graph to display hit heatmaps, chronological scrubbing, and impact graphs.

---

## File Map

- `cmd/cadr/`: CLI entrypoints (`main.go`, `api_cmd.go`, `trace_cmd.go`).
- `internal/graph/`: In-memory directed graph storing directories, files, functions, and endpoints.
- `internal/analyzer/`: Project directory walker, lexical proximity resolution (`findCallee`, `findCaller`), and transitive BFS dependency analysis (`impact.go`).
- `internal/lang/`: Language configuration interfaces and Tree-sitter grammar bindings (Python, Go, JavaScript, C).
- `queries/`: Tree-sitter Scheme queries (`.scm`) for AST extraction.
- `internal/tracer/`: IPC socket listener, process supervisor, and run storage engine (`run_store.go`).
- `internal/agents/`: Embedded runtime hooks (`py_trace.py`, `js_trace.js`, `sitecustomize.py`) plus the vendored JS parsers (`acorn.js`, `acorn-walk.js`); bump them with `scripts/vendor-acorn.sh <acorn-version> <acorn-walk-version>` (regenerates the provenance headers and licenses).
- `internal/toolwrap/`: Go build-time toolchain interception, AST rewriter (`instrument.go`), and runtime templates (`runtime_template.go`).
- `internal/frameworks/`: Static HTTP route extractors for Flask, FastAPI, and Gin.
- `internal/tui/`: Terminal user interface implementations (main explorer, API explorer).
- `internal/server/`: Model Context Protocol (MCP) server implementation for AI agents.

---

## Development Setup

### Prerequisites
- Go 1.22 or higher.
- A C compiler (e.g., `gcc` or `clang`) for CGO bindings required by Tree-sitter.

### Building and Testing
```bash
# Clone repository
git clone https://github.com/abhinavdevarakonda/cadr.git
cd cadr

# Run full test suite
go test ./...

# Build the binary locally
go build -o cadr ./cmd/cadr

# Run with local binary
./cadr .
```

---

## Extension Recipes

### Recipe 1: Add a Static Language Parser
1. Add the Tree-sitter grammar dependency to `go.mod`.
2. Create an AST extraction query in `queries/<language>.scm` defining captures for function definitions (`@function.def`, `@method.def`) and function call sites (`@call`).
3. Implement the `LanguageConfig` interface in `internal/lang/<language>/config.go`.
4. Register the new language config in `internal/analyzer/analyzer.go`.

### Recipe 2: Add an API Framework Detector
1. Create Tree-sitter queries in `queries/<framework>.scm` targeting route decorators or registration methods.
2. Implement route and parameter extraction logic in `internal/frameworks/<framework>.go`.
3. Register the detector in `cmd/cadr/api_cmd.go` inside the framework scanning loop.
4. Add unit tests covering route detection, nested groups/blueprints, and path parameter extraction in `internal/frameworks/<framework>_test.go`.

### Recipe 3: Add an MCP Tool
1. Define the tool schema, name, and input arguments in `internal/server/mcp.go`.
2. Implement the tool handler function in `internal/server/mcp.go`, querying the active graph or trace store.
3. Add the tool to the `tools/list` response handler.
4. Update `docs/mcp.md` to document the new tool and its parameter contract.

---

## Pull Request Guidelines
- Ensure all tests pass: `go test ./...`.
- Preserve documentation and existing comments.
- Do not introduce external dependencies into runtime agent hooks (`internal/agents/` and `internal/toolwrap/runtime_template.go`); runtime agents must remain self-contained.
- Do not commit generated binaries (`/cadr`), scratch files, or test outputs.
