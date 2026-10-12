# cadr MCP Server: AI Agent Integration Guide

cadr exposes its static dependency graph, AST index, and dynamic execution traces via the Model Context Protocol (MCP).

This allows AI coding agents (Claude Desktop, Cursor, Antigravity, Windsurf) to query exact symbol definitions, compute call hierarchies, perform impact blast-radius calculations, and inspect ground-truth runtime parameters over standard JSON-RPC.

---

## Client Configuration

### Claude Desktop
Add cadr to `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

```json
{
  "mcpServers": {
    "cadr": {
      "command": "cadr",
      "args": ["mcp"]
    }
  }
}
```

### Cursor
Add cadr under Cursor Settings -> Features -> MCP Servers -> Add New MCP Server:
- Name: `cadr`
- Type: `command`
- Command: `cadr mcp`

### Antigravity / Gemini CLI
Add cadr to your client configuration:

```json
{
  "mcpServers": {
    "cadr": {
      "command": "cadr",
      "args": ["mcp"]
    }
  }
}
```

---

## Available MCP Tools

cadr exposes 17 tools grouped into three operational categories:

### 1. Static Symbol & Graph Navigation
- `set_project_root`: Point cadr at an active codebase root directory to rebuild or switch graph indexing.
- `find_symbol`: Resolve an ambiguous function or method name into qualified node IDs and file coordinates.
- `get_node_details`: Return metadata for a specific node (type, file path, start line, end line).
- `get_node_source`: Extract the exact source code for a function directly from disk without reading the whole file.
- `get_file_symbols`: List all function and method symbols declared within a single file.

### 2. Dependency & Impact Analysis
- `get_callers`: Return direct callers and the exact line numbers where the target is invoked.
- `get_callees`: Return direct outgoing dependencies (functions called by this target).
- `impact_analysis`: Compute the upstream blast radius (transitive callers) affected if a target function changes.
- `trace_calls`: Compute the full downstream dependency tree (transitive callees).
- `call_path`: Find the shortest call path between two functions (e.g. `api.handler -> auth.verify -> db.query`).

### 3. Dynamic Tracing & Verification
- `run_trace`: Execute an arbitrary test or run command in the background and return function hit summaries and exit codes.
- `get_last_trace`: Retrieve the most recent trace recorded by `cadr rec`. Returns execution counts, total times, and runtime arguments.
- `trace_runs`: List all stored trace runs in `.cadr/traces/`.
- `trace_summary`: Return aggregated invocation counts, total duration, and self-time for a run.
- `trace_tree`: Return the nested execution call tree of a run.
- `trace_diff`: Return the behavioral and latency delta between two recorded runs.
- `trace_verify`: Assert whether a recorded run matches expected call chains and AST nodes.

---

## Recommended Agent Workflow

When asking an AI agent to inspect or modify code, prompt it to leverage cadr:

1. **Locate and Scope:**
   "Use find_symbol to locate JoinQueue, then call get_node_source to inspect its implementation."
2. **Assess Blast Radius Before Editing:**
   "Run impact_analysis on handlers.JoinQueue to see all higher-level routes or services that could break if we change its signature."
3. **Verify Runtime Execution:**
   "Run `run_trace` with `go test ./handlers/...` and verify which database calls were executed."

---

## Context File Snippet (`AGENTS.md` / `CLAUDE.md`)

Drop this snippet into your repository's `AGENTS.md` or `CLAUDE.md` so models automatically use cadr rather than slow full-directory grep searches:

```markdown
### Code Intelligence with cadr
This repository is indexed by cadr. When researching code structure:
- Use `cadr` MCP tools (`find_symbol`, `get_node_source`, `get_callers`, `get_callees`) rather than reading entire files or running broad grep searches.
- Before refactoring functions, use `impact_analysis` to evaluate the upstream caller chain.
- To inspect execution paths, use `trace_calls` (static) or `get_last_trace` (dynamic runtime).
```
