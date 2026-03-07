# Maplet

Maplet is a terminal-native tool for exploring codebase structure and call relationships.

When working in an unfamiliar project, Maplet helps you answer:
- **Can I safely modify this function?**
- **What does this actually trigger?**
- **How is this function connected to the rest of the codebase?**

Maplet builds a graph of directories, files, and functions, allowing you to navigate relationships without leaving the terminal or guessing where a function is used.

---

## Installation

### Via `go install`
```bash
go install github.com/abhinavdevarakonda/maplet/cmd/maplet@latest
```

---

## Features

### Project Navigator
Interactive TUI for exploring your code's structure and dependencies.
```bash
maplet nav [path]
```

### Impact Analysis
Find every function that depends on a specific symbol.
```bash
maplet impact <symbol>
```

### AI Integration (MCP)
Start an MCP server to let AI assistants (Claude, Cursor, etc.) query your project's graph logic.
```bash
maplet mcp <path>
```

### Graph Export
Export the project's internal call graph as JSON for external analysis.
```bash
maplet export <path>
```

### Web Visualizer
Starts a local web server to visualize the code graph in your browser.
```bash
maplet serve [path]
```

---

## TUI Controls

| Key | Action |
| :--- | :--- |
| `j` / `k` | move selection up and down |
| `h` / `l` | expand/Collapse directories or focus between panes |
| `i` | toggle focus between structure and impact/trace panes |
| `t` | toggle right pane between **impact** (callers) and **trace** (callees) |
| `enter` | open function in your default `$EDITOR` (jumps to exact line) |
| `q` | quit |

---

## How it Works
Maplet performs static analysis to construct a weighted graph of:
- **structure**: Which directories hold which files, and which files hold which functions.
- **calls**: Which functions invoke which symbols, including cross-package calls.

The TUI uses this graph to provide a "live" map, while the **MCP server** exposes tools like `find_symbol`, `impact_analysis`, and `call_path` to AI agents.
