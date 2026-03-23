# Maplet

Maplet is a terminal-native tool that integrates static analysis with dynamic execution tracing. It shows you a real-time map of your codebase's execution flow directly in the terminal. Explore complex application logic and legacy codebases without relying on manual logs, or "KT's" if you're using it at work :p

Maplet has three primary systems:
1. **Static Analyzer (TreeSitter)**: Parses the project directories to build a weighted structural dependencies graph.
2. **Dynamic Tracer (Zero-Code Instrumentation)**: Uses `PYTHONPATH` injection (or `sitecustomize.py`) to hook into target applications, transmitting TCP payloads of your service line-by-line back to the Maplet daemon. Native support for multi-threading, multi-processing, and web subprocesses (Flask, Uvicorn, Django).
3. **Interactive TUI**: Sticks and follows to the live execution feed, providing a TUI for navigating execution paths chronologically or structurally in a way vim users can appreciate.

---

## Installation

```bash
go install github.com/abhinavdevarakonda/maplet/cmd/maplet@latest
```

---

## Usage Overview

Maplet operates in a dual-pane workflow. The left pane acts as the "body", while the right pane is the "brain", tracing the flow the codebase takes during runtime.

**1. Start the Maplet Daemon:**
```bash
# Analyze the current directory and start listening for trace payloads
maplet flow .
```

**2. Execute Your Application:**
```bash
# Maplet will inject the environment variables and execute the process
maplet run "python app.py"
```
*(you can substitute `python app.py` based on your framework; `flask run`, `uvicorn main:app`, `pytest`, etc.)*

---

## Technical Features

### Real-time Trace Visualization (`maplet flow`)
As the process executes, Maplet maps the incoming trace payloads onto the pre-made static graph:
- **Heatmap Visualization**: Nodes in the structure tree visually scale based on execution frequency (function hit counts).
- **Chronological Playhead**: The right-hand pane records a linear sequence of execution events.
- **Dynamic Unrolling**: The structural tree (Left Pane) will automatically unroll directories and snap focus to follow the active execution context in real-time.
- **Fallback Navigation**: if your app executes dynamically generated code like a Jinja2 template that Maplet's static graph can't find, it will still capture the exact file and line number so you can instantly open it in your editor by just hitting enter on the function.

### Advanced TUI Controls
Maplet uses standard Vim keybindings (`j/k/h/l`, `gg/G`, `/` for fuzzy search, `ctrl+j/k` for jumps).

Here are the Maplet-specific bindings:

| Key | Action |
| :--- | :--- |
| `i` | Toggle focus between the Left (Structure) and Right (Trace/Impact) panes. |
| `c` | **State Toggle**: Collapse the entire tree to the root. Press again to restore your previous exact folder state. |
| `enter` | Open the selected function/file in your `$EDITOR` precisely at that line number. |
| `f` | Toggle **Follow Live**: Stops the left pane from auto-scrolling through the real-time function calls so you can inspect code while the app runs. |
| `Space` | Toggle **Live/Pause**: Pause the UI to inspect a specific moment in the trace without losing data. Press again to unpause and go back to current execution calls. |
| (shift+) `H` / `L` | Time-travel scrub backward/forward through the chronological trace history. |
| `t` | Cycle right pane between **Flow** (sequence), **Impact** (callers), and **Trace** (callees). |


---

## Maplet MCP Server

Maplet exposes its static graph and dynamic analysis tools as a Model Context Protocol (MCP) server. Instead of forcing AI assistants (like Claude Code, Cursor, or Antigravity) to blindly `grep` through thousands of files or guess what functions were called based on standard terminal output, Maplet feeds them exact deterministic structures and live execution traces. This reduces token usage, prevents the AI from "hallucinating" how code connects, and allows them to debug complex runtime errors instantly.

To use Maplet with your AI agent, add the following to your agent's MCP configuration JSON file:

```json
{
  "mcpServers": {
    "maplet": {
      "command": "maplet",
      "args": [
        "mcp",
        "/absolute/path/to/your/project"
      ]
    }
  }
}
```
