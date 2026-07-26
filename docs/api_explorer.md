# cadr API Explorer (`cadr api`)

`cadr api` is a terminal API client (similar to Postman or curl, but integrated with your codebase) that automatically discovers your backend routes, and lets you execute HTTP requests.

---

## Features
*   **Automatic Route Discovery:** Statically scans your codebase to find endpoints using Tree-sitter AST queries. 
*   **Vim-Style Navigation:** vim-like Keybindings for navigating, inputting params, and inspecting responses.
*   **Smart Parameter Persistence:** Automatically caches headers, parameters, path variables, and request body inputs per endpoint.

> Note: cadr currently supports `Flask` and `FastAPI` (because I use those more frequently right now, but some NodeJs frameworks are on the way too!) 

---

## Getting Started

To launch the API Explorer, run the following command in your project's **root directory**:

```bash
cadr api
```

`cadr` will search for a cached route definition file at `.cadr/cache/endpoints.json`. If there aren't any, a static AST scan of your codebase will find your routes.

---

## Layout Overview

The TUI is divided into three key panels:


*   **Left pane:** Shows the list of discovered endpoints/routes, with more options for each route.
*   **Right pane:** Shows the responses and call graphs for each endpoint/route as you hover over them in the left pane.

---

## Keybindings & Controls

### Navigation & Focus
*   `j` / `k` (or `Up` / `Down` Arrow): Scroll up and down lists (left pane) and text fields/responses (right pane).
*   `t` / `Tab`: Toggles through modes of the right pane between call graph to endpoint response for that route.
*   `h` / `l` (or `Left` / `Right` Arrow): Switch focus between the Left Pane (endpoints list) and the Right Pane (Response / Call Graph). Go back to main route list after inspecting a specific route by also pressing `h`.
*   `i`: `insert` mode in text areas.

### Executing Requests
*   **`Ctrl+s` (Send Request):** Pressing `Ctrl+s` from anywhere in the TUI fires the HTTP request to your local dev server.
*   When a request is executed, your parameters, headers, query inputs, and body values are automatically saved to `.cadr/cache/api_cache.json` so they are pre-filled the next time you hit this endpoint.

### Right Pane Tab Control
When the Right Pane is focused:
*   **`t` or `Tab`:** Cycles the right-hand panel view mode:
    *   **Response Tab:** Shows the raw HTTP status code, roundtrip execution latency, response headers, and response body.
    *   **Call Graph Tab:** Shows you trace of the functions executed on your backend server to handle this specific request.
*   **`Enter` to open response:** press `enter` on the right pane after hitting an endpoint to open the response in a neovim buffer. 
        >I specifically use this for bigger responses to save this file in one place to compare later.
*   **Scroll Response:** Use `j` and `k` to scroll up and down the response body.
*   **Fuzzy Text Search (`/`):** Press `/` to trigger the response search bar.
*   **Copy Response Body (`y`):** Copies the raw HTTP response body to your system clipboard (uses `pbcopy` on macOS; `xclip`/`xsel`/`wl-copy` on Linux; `clip` on Windows).

---

<!-- ## Endpoint Detection Details -->
<!---->
<!-- `cadr` parses your codebase statically using AST queries: -->
<!---->
<!-- ### Flask -->
<!-- *   **Query File:** [queries/flask.scm](file:///Users/abee/code/me/cadr/queries/flask.scm) -->
<!-- *   **Scans for:** Blueprints (`Blueprint(...)`), route decorators (`@app.route()`, `@bp.get()`), and extracts route variables like `<int:book_id>`. -->
<!---->
<!-- ### FastAPI -->
<!-- *   **Query File:** [queries/fastapi.scm](file:///Users/abee/code/me/cadr/queries/fastapi.scm) -->
<!-- *   **Scans for:** APIRouter declarations (`APIRouter(...)`), endpoint decorators (`@app.post()`, `@router.get()`), and path templates like `{book_id}`. -->
