# cadr MCP Server: AI Agent Integration Guide

cadr exposes its static dependency graph, AST index, and dynamic execution traces via the Model Context Protocol (MCP).

This allows AI coding agents (Claude Desktop, Cursor, Antigravity, Windsurf) to query exact symbol definitions, compute call hierarchies, perform impact blast-radius calculations, and read recorded execution traces over standard JSON-RPC.

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

Runs are recorded with `cadr rec "<command>"` in a shell; the MCP server never executes commands. Read a run in three tiers, never as one dump:

**Digest → index → drill.** `trace_summary` (~30 lines: what ran, how slow, what failed), then `trace_context` (one line per request, hottest first, labelled with its HTTP route), then `trace_tree` scoped by `ctx=<id>`, `fn=<name>` or `route=<label>`. A whole-run tree of a busy server is thousands of interleaved rows and is refused by default.

- `get_last_trace`: The most recent run. Alias of `trace_summary(run="last")` — prints the run id, joins the static graph, and includes errors.
- `trace_runs`: List all stored runs in `.cadr/traces/runs/` (id, start time, duration, exit code, events, error count).
- `trace_summary`: Aggregated counts, total duration, self time and source location per function, plus `errors:`, `warnings:` and `trace anomalies:` sections.
- `trace_context`: Execution contexts (goroutine/thread/task) of a run — the request index.
- `trace_tree`: Indented call tree, scoped by `ctx`, `fn` or `route`.
- `trace_diff`: Behavioral and latency delta between two recorded runs, including the first divergence.
- `trace_verify`: Assert whether a run's internal invariants hold (span pairing, timing, seq integrity) plus the recorded errors.

### Machine-readable output

Every trace tool returns both a text rendering (what the model reads) and a
`structuredContent` envelope with the same numbers: `run_id`, `cmd`, `exit_code`,
`complete`, `wall_ns`, `trace_ns`, `events` / `enter` / `exit`, `errors`, and the
`full_run_file` path. `trace_summary` also carries the per-function `functions` array plus
`warnings` / `anomalies`; `trace_context` carries the `contexts` array; `trace_tree` carries the
`scope` it covers; `trace_verify` carries `ok` / `counts` / `violations`; `trace_diff` carries the
full diff object. Nothing has to be parsed out of padded columns.

### Errors are first-class

A panic or uncaught exception is recovered from the run's stderr, localized to a `file:line`, stored in `runs/<id>.errors.json`, and reported by every trace tool. A crashed run can no longer look healthy: in the TUI the failing function turns red with a `✗`, and `cadr trace summary` prints an `errors:` section.

---

## Recommended Agent Workflow

When asking an AI agent to inspect or modify code, prompt it to leverage cadr:

1. **Locate and Scope:**
   "Use find_symbol to locate JoinQueue, then call get_node_source to inspect its implementation."
2. **Assess Blast Radius Before Editing:**
   "Run impact_analysis on handlers.JoinQueue to see all higher-level routes or services that could break if we change its signature."
3. **Verify Runtime Execution:**
   "Record a run with `cadr rec \"go test ./handlers/...\"`, call `trace_summary` to see whether it failed and where, then `trace_tree(ctx=<id>)` or `trace_tree(fn=<name>)` for the path."

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
