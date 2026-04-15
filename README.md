# Maplet

<video src="https://github.com/user-attachments/assets/7d523ad0-d2a6-403e-a879-e9f3cd1344e0" autoplay loop muted></video>

Maplet is a terminal-native tool that integrates static analysis with dynamic execution tracing. It shows you a real-time map of your codebase's execution flow directly in the terminal. Explore complex application logic and legacy codebases without relying on manual logs, or "KT's" if you're using it at work :p

Maplet has three primary systems:
1. **Static Analyzer (TreeSitter)**: Parses the project directories to build a weighted structural dependencies graph.
2. **Dynamic Tracer**: Uses `PYTHONPATH` or `NODE_OPTIONS` injection to hook into target applications, transmitting TCP payloads of your service line-by-line back to the Maplet daemon. Native support for multi-threading, multi-processing, and web subprocesses (Flask, Uvicorn, Express, NestJS).
3. **Interactive TUI**: Sticks and follows to the live execution feed, providing a TUI for navigating execution paths chronologically or structurally in a way vim users can appreciate.

---

## Installation

### linux/macOS

#### Download Binary
Download latest binary from [Github Releases.](https://github.com/abhinavdevarakonda/maplet/releases) 

#### go install
```bash
go install github.com/abhinavdevarakonda/maplet/cmd/maplet@latest
```

#### Build from Source
> Requires a C compiler due to CGO (treesitter)
```bash
git clone https://github.com/abhinavdevarakonda/maplet
cd maplet
go build -o maplet ./cmd/maplet
sudo mv maplet /usr/local/bin/ # Add to PATH
```

### Windows

#### WSL (Recommended)
Follow the Linux instructions inside your WSL terminal.

#### Native PowerShell
1. **Prerequisite:** Install a C compiler (e.g., `scoop install gcc` or `choco install mingw`).
2. **Install:**
```powershell
go install github.com/abhinavdevarakonda/maplet/cmd/maplet@latest
```
*Note: the TUI would look cleaner and more consistent if using Windows Terminal.*

---

## Usage Overview
> Note: Maplet currently provides full dynamic execution tracing for Python and JavaScript/TypeScript. Static analysis and project navigation are supported for Python, Go, C, and JavaScript/TypeScript. Additional languages will be added soon! (I'm sorry i just deal with those more than others, so I had to add them first.)

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
*(you can substitute `python app.py` based on your framework; `node app.js`, `npm start`, `flask run`, `uvicorn main:app`, `pytest`, etc.)*

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

Maplet exposes its static graph and dynamic analysis tools as an **MCP (Model Context Protocol)** server. This allows AI assistants (like Claude, Cursor, or Antigravity) to "see" your code structure and follow execution flows without manual grepping.

### Setup
Add Maplet to your agent's MCP configuration (e.g., `claude_desktop_config.json`). 

```json
{
  "mcpServers": {
    "maplet": {
      "command": "maplet",
      "args": ["mcp"]
    }
  }
}
```

### AI-Agent Power Tools
Maplet provides the AI with several high-level tools to understand code deeply:
- **`find_symbol`**: Locates function definitions across the entire project.
- **`get_callers / get_callees`**: Shows the immediate "Impact" and "Trace" of a function.
- **`run_trace`**: Lets the AI execute your code and see the **live call sequence** returned as data.
- **`set_project_root`**: Allows the AI to autonomously switch its focus to a different project or subdirectory as you move around.
