# Maplet

> Trace your code in real-time

<video src="https://github.com/user-attachments/assets/7d523ad0-d2a6-403e-a879-e9f3cd1344e0" autoplay loop muted></video>

Maplet is a terminal-native tool that combines static analysis with runtime tracing to show a real-time map of your codebase as it executes. It helps you follow the flow of functions, services, and data directly in your terminal, so you can understand complex systems without piecing things together manually.

Whether you live in the terminal or you've been *burning through too many tokens*. It helps you get through KT sessions, debug messy systems, explore legacy codebases, and figure out what's actually going on without digging through muddled, messy logs.

Maplet has three primary systems:
1. **Static Analyzer (TreeSitter)**: Parses the project directories to build a weighted structural dependencies graph.
2. **Dynamic Tracer**: Uses `PYTHONPATH` injection (or `sitecustomize.py`) to hook into target applications, transmitting line-by-line execution data over TCP back to the Maplet daemon. Native support for multi-threading, multi-processing, and web subprocesses (Flask, Uvicorn, Django).
3. **Interactive TUI**: Follows the live execution feed, providing a TUI for navigating execution paths chronologically or structurally in a way vim users can appreciate.

---

## Installation

### Download Binary
Download latest binary from [Github Releases.](https://github.com/abhinavdevarakonda/maplet/releases) 
### Build from Source
> Requires a C compiler due to CGO (treesitter)
```bash
git clone https://github.com/abhinavdevarakonda/maplet
cd maplet
go build -o maplet ./cmd/maplet
sudo mv maplet /usr/local/bin/ # Add to PATH
```

### go install
```bash
go install github.com/abhinavdevarakonda/maplet/cmd/maplet@latest
```

---

## Usage Overview
> Note: Maplet currently provides full dynamic execution tracing for Python. Static analysis and project navigation are supported for Python, Go, and C. Additional languages will be added soon! (I'm sorry i just deal with those more than others, so I had to add them first.)

Maplet operates in a dual-pane workflow. The left pane acts as the "body", while the right pane is the "brain", tracing the flow the codebase takes during runtime.

**1. Start the Maplet Daemon**:
```bash
# Analyze the current directory and start listening for trace payloads
maplet flow .
```

**2. Execute Your Application**:
```bash
# Maplet will inject the environment variables and execute the process
maplet run "python app.py"
```
*(you can substitute `python app.py` based on your framework; `flask run`, `uvicorn main:app`, `pytest`, etc.)*

### Other Quick Tools (WIP)
> Maplet will show the whole codebase structure through [cytoscape.js](https://js.cytoscape.org/) at `localhost:6433`
```bash
maplet serve .
```

---

## Technical Features

### Real-time Trace Visualization (`maplet flow`)
As the process executes, Maplet maps the incoming trace payloads onto the pre-made static graph:
- **Heatmap Visualization**: Nodes in the structure tree visually scale based on execution frequency (function hit counts).
- **Chronological Playhead**: The right-hand pane records a linear sequence of execution events.
- **Dynamic Unrolling**: The structural tree (left pane) will automatically jump through directories to focus on active function/section in the real time trace execution.
- **Fallback Navigation**: if your app executes dynamically generated code like a Jinja2 template that Maplet's static graph can't find, it will still capture the exact file and line number so you can instantly open it in your editor by just hitting enter on the function.

### Advanced TUI Controls
Maplet uses standard Vim keybindings (`j/k/h/l`, `gg/G`, `/` for fuzzy search, `ctrl+j/k` for jumps).

These are the Maplet-specific bindings:

| Key | Action |
| :--- | :--- |
| `i` | Toggle focus between the Left (Structure) and Right (Trace/Impact) panes. |
| `c` | **State Toggle**: Collapse the entire tree to the root. Press again to restore your previous exact folder state. |
| `enter` | Open the selected function/file in your `$EDITOR` precisely at that line number. |
| `f` | Toggle **Follow Live**: Stops the left pane from auto-scrolling through the real-time function calls so you can inspect code while the app runs. |
| `Space` | Toggle **Live/Pause**: Pause the UI to inspect a specific moment in the trace without losing data. Press again to unpause and go back to current execution calls. |
| (shift+) `H` / `L` | scrub backwards or forwards through the chronological trace history. |
| `t` | Cycle right pane between **Flow** (sequence), **Impact** (callers), and **Trace** (callees). |


---

## Maplet MCP Server

Maplet exposes its static graph and dynamic analysis tools as a Model Context Protocol (MCP) server. Instead of forcing AI assistants (like Claude Code, Cursor, or Antigravity) to blindly `grep` through thousands of files or guess what functions were called based on standard terminal output, Maplet feeds them exact structures and live execution traces. This reduces token usage, prevents the AI from "hallucinating" how code connects, and allows them to debug complex runtime errors instantly.

To use Maplet with your AI agent, add the following to your agent's MCP configuration JSON file:
> Replace `/absolute/path/to/your/project` with the actual path to your project.
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
`
