# cadr API Explorer (`cadr api`)

<p align="center">
  <img src="../assets/cadr-api-demo.gif" alt="cadr api explorer demo" />
</p>

`cadr api` is a terminal-native API client integrated directly into your codebase. It statically discovers your backend HTTP routes, extracts path and query parameters, maps the underlying handler functions, and allows firing requests directly from the terminal.

---

## Supported Frameworks

cadr currently supports automatic route and parameter detection for:

- **Go:** Gin (`gin-gonic/gin`)
  - Detects all HTTP methods (`GET`, `POST`, `PUT`, `DELETE`, `PATCH`, `OPTIONS`, `HEAD`, `Any`).
  - Resolves nested router groups (`r.Group(...)`).
  - Resolves constant paths, variables, and string concatenations.
  - Extracts path parameters (`:id`, `*filepath`).
  - Cross-references route declarations to handler functions defined in other packages.
- **Python:** FastAPI
  - Resolves `APIRouter` prefixes and nested router includes.
  - Detects HTTP method decorators (`@router.get`, `@app.post`, etc.).
  - Extracts path parameters (`{item_id}`) and type annotations.
- **Python:** Flask
  - Resolves Blueprints and blueprint url prefixes.
  - Detects `@app.route` and `@bp.route` decorators with explicit `methods=[...]`.
  - Extracts typed path parameters (`<int:id>`, `<string:slug>`).

---

## Getting Started

Launch the API Explorer from your repository root:

```bash
cadr api
```

cadr scans your project files using Tree-sitter AST queries, identifies your framework, extracts all routes, and defaults the target URL to your local dev port (e.g., `http://localhost:8080` for Gin, `http://localhost:8000` for FastAPI, `http://localhost:5000` for Flask).

You can override the target base URL using the `PORT` environment variable or the top URL bar inside the TUI.

---

## Layout Overview

The interface is organized into a dual-pane layout:

- **Left Pane (Endpoints List):** Displays all discovered HTTP routes grouped by method and path. Selecting a route displays its parameter inputs and handler details.
- **Right Pane (Response & Call Graph):** Toggles between the live HTTP response and the static call graph of the handler function associated with the selected route.

---

## Keybindings and Controls

### Navigation
- `j` / `k` (or `Up` / `Down`): Navigate through the endpoint list (left pane) or scroll response bodies (right pane).
- `h` / `l` (or `Left` / `Right`): Switch active focus between the endpoints pane and the response/inspection pane.
- `Tab` / `t`: Switch between the HTTP Response view and the static Call Graph view in the right pane.
- `i`: Enter insert mode on parameter text inputs (path parameters, query parameters, headers, JSON body).
- `Esc`: Exit insert mode and return to normal navigation.

### Request Execution and Caching
- `Ctrl+s`: Send the HTTP request to the target server.
- **Automatic Parameter Persistence:** Every input entered (path variables, query parameters, header values, body payloads) is cached per-endpoint in `.cadr/cache/api_cache.json`. When you return to an endpoint, your previous inputs are restored automatically.
- **Path Parameter Substitution:** Variables in paths (such as `/games/:id` or `/users/{user_id}`) are automatically substituted with the values you input before the HTTP request is dispatched.

### Response Inspection
- `Enter`: Open the response body in your `$EDITOR` (e.g. Neovim) for full-screen analysis or diffing.
- `/`: Open the response search bar to fuzzy-filter text inside large JSON payloads.
- `y`: Copy the raw response body to the system clipboard (`pbcopy` on macOS, `wl-copy`/`xclip` on Linux, `clip` on Windows).

---

## Dynamic Endpoint Caching

Endpoint definitions discovered during scans are cached in `.cadr/cache/endpoints.json`. To force a full re-scan of routes (for instance, after adding new routes), delete this cache file or run `cadr api` with updated source files.
