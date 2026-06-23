# cadr API Explorer (`cadr api`)

`cadr api` is a terminal-native API client (similar to Postman or curl, but integrated with your codebase graph) that automatically discovers your backend routes and traces their execution paths so you can jump into the code that handles each request and even the functions it calls.

---

## Getting Started

To launch the API Explorer, just run the following command in your project's root directory:

```bash
cadr api
```

---

## Layout Overview

The TUI is divided into three zones:

1. **Left Pane (endpoint registry):** Discovered routes parsed from your source files.
2. **Right Pane (Dynamic/Static Trace & Response):** Shows the execution trace of the request, or if you hit the endpoint, the raw HTTP response.
3. **Bottom Bar (Request Builder):** Fields to configure the URL path parameters, query parameters, HTTP headers, and JSON request bodies, all fetched from your codebase.

---

## Key Actions & Controls

The API Explorer supports standard Vim keybindings for navigation, with keybindings for request firing, response searching, and clipboard copying.
> for the clipboard copying feature, you do need to have xclip installed on linux, but should work fine with macOS and windows

### Navigation & Focus
* `j` / `k` (or `Up` / `Down` Arrow): Scroll up and down.
* `Tab` / `Shift+Tab`: Cycle focus sequentially through interactive elements:
* `h` / `l` (or `Left` / `Right` Arrow): Switch focus between the Left Pane (endpoints list) and the Right Pane (response / call graph).
* `t` / `Tab` (when right pane is focused): Switch the right tab view between **Response** and **Call Graph**.

### Firing Requests
* Press `Ctrl+s` from anywhere (inputs or lists) to send/execute the request. 
This will also automatically save your *path parameters*, *query parameters*, *headers*, and *body* for that endpoint so they persist when you return to it later. The TUI runs the request concurrently in the background.

### Response Pane Controls
When the **Response** tab is focused (`l` to focus, then `t` to toggle to Response):
* **Scroll Response:** Press `j` and `k` to scroll up and down. Long headers or body contents will automatically word-wrap.
* **Fuzzy Text Search (`/`):** Press `/` to trigger the response search bar. Type any string to instantly find and highlight matches in real-time (using ANSI color styling). Press `Enter` or `Esc` to close the search overlay.
* **Copy Response Body (`y`):** Press `y` to copy the entire raw HTTP response body directly to your system clipboard (requires `xclip`, `xsel`, or `wl-copy` on Linux; works out-of-the-box on macOS and Windows).
