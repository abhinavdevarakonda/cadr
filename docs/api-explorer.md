# cadr API Explorer (`cadr api`)

<p align="center">
  <img src="../assets/cadr-api-demo.gif" alt="cadr api explorer demo" />
</p>

`cadr api` is a terminal-native API client integrated directly into your codebase. It statically discovers your backend HTTP routes, extracts path and query parameters, maps the underlying handler functions, and allows firing requests directly from the terminal.

---

## Supported Frameworks

cadr currently supports automatic route and parameter detection for:

- **Go:** Gin (`gin-gonic/gin`)
  - Detects all HTTP methods (`GET`, `POST`, `PUT`, `DELETE`, `PATCH`, `OPTIONS`, `HEAD`); `Any` expands to `GET`/`POST`/`PUT`/`PATCH`/`DELETE`.
  - Resolves nested router groups (`r.Group(...)`).
  - Resolves constants declared in any file of the same package, plus exported constants in other packages through their import path (`config.Prefix`).
  - Resolves config-field defaults (`cfg.APIBase`): when every struct literal in the project agrees on a field's value, that value is used as the group prefix.
  - An empty route path stays at its group base (`books.GET("")` is `/api/v1/books`, not `/api/v1/books/`).
  - Extracts path parameters (`:id`, `*filepath`).
  - Cross-references route declarations to handler functions defined in other packages.
- **Python:** FastAPI
  - Resolves `APIRouter(prefix=...)`, same-file `app.include_router(router, prefix=...)`, and cross-file `app.include_router(module.router, prefix=...)` prefixes.
  - An empty decorator path joins to its prefix without a trailing slash (`@router.get("")` is `/api/v1/books`).
  - Detects HTTP method decorators (`@router.get`, `@app.post`, etc.).
  - Resolves module-level constants imported with `from x import Y`, `from x import Y as Z`, `import x as y`, and `os.getenv("X", "/default")`.
  - Resolves config-field defaults (`settings.api_base` for a dataclass/pydantic field), see below.
  - Extracts path parameters (`{item_id}`) and type annotations.
- **Python:** Flask
  - Resolves Blueprints and blueprint url prefixes.
  - Detects `@app.route` and `@bp.route` decorators with explicit `methods=[...]`.
  - Resolves the same imported/module-level constants as FastAPI, including f-string paths.
  - Extracts typed path parameters (`<int:id>`, `<string:slug>`).
- **JavaScript / TypeScript:** Express, Fastify, Koa-router
  - Express: `app.get/post/put/patch/delete/options/head`, `app.all` (expands to five verbs), `app.use(prefix, router)` prefixes, array paths (`app.get(['/a','/b'], h)`), and string-literal/`const`/`+`/template-literal paths.
  - Fastify: method calls plus the object form `fastify.route({ method, url, handler })` (`method` may be an array), and `fastify.register(plugin, { prefix })`.
  - Koa-router: same call shape as Express, plus `router.prefix('/api')`.
  - Path constants are followed across files: `require`/`import` (named, renamed, namespace, destructured), `module.exports` object re-exports, `export const`, and `export ... from './m'` hops.
  - Prefixes from `app.use(...)`/`fastify.register(...)` are resolved in the same file only; a router mounted in `server.js` and declared in `routes/users.js` is listed without the mount prefix.
  - **Not supported yet:** NestJS (`@Controller` + `@Get` on class methods).

---

## How path constants are resolved

A route path does not have to be one literal. For every language the same order
applies:

1. **File-local constants** — `const BASE = '/api'; const PREFIX = BASE + '/v1'`,
   in any declaration order (`:=` locals included for Go).
2. **Imported values** — follow the import/require to the declaring module and
   read the exported literal. One re-export hop is supported (depth 4,
   cycle-safe). Supported shapes: `require`, named/renamed/namespace/default
   `import`, destructured `require`, `module.exports = { ... }`,
   `exports.X = ...`, `export const`, `export { X } from './m'`, Go's
   `pkg.Const` via its import path, Python's `from .settings import X` /
   `from pkg.settings import X as Y` / `import pkg.cfg as cfg`.
3. **Config-field defaults** — when a path reads a struct/dataclass field
   (`cfg.APIBase`, `settings.api_base`), the field's statically-known default is
   used if every known default agrees. A runtime env/config override is *not*
   followed (the static default wins), and conflicting defaults keep the
   fillable `[NAME]` token.
4. **Unique project-wide name** — if exactly one value declaration of the name
   exists, use it. Two declarations with the same name are treated as an
   ambiguity, never guessed.
5. **Placeholder** — see below.

Literal widening is applied at every step: `X || '/default'`, `X ?? '/default'`,
`/x' as const`, `satisfies`, non-null assertions, `` `${X}/books` `` (JS),
`f"{X}/books"` (Python), and `os.getenv("X", "/default")` /
`os.environ.get("X", "/default")` (Python).

Not resolved (deliberately): cross-file mount graphs
(`app.use('/api', require('./routes'))`), nested cross-file `include_router`
chains (`app.include_router(a.router)` → another include), dynamically computed
routes, chained `router.route('/x').get(h)`, regex paths, and NestJS.

---

## Unresolved path segments

When a segment cannot be resolved statically, the list keeps the endpoint
visible and marks the hole instead of showing a broken URL:

```
GET  /[API_PREFIX]/books
```

- The endpoint appears in the list with the `[NAME]` token in place; a value
  that is not a plain identifier (a call, conditional, …) becomes
  `[exprL<line>]`.
- The request builder shows a field for that name, prefilled from the cache
  when you have filled it before. Its placeholder says
  `unresolved in source — e.g. /api/v1`.
- `Enter` focuses the field, type the real value (for example `api/v1`), and
  `Ctrl+s` sends the substituted URL. When the token is preceded by `/` and your
  value starts with `/`, one slash is dropped so the URL never becomes `//`.
- The value is persisted per endpoint in `.cadr/cache/api_cache.json`, together
  with the other inputs, so you only fill it once.

---

## Getting Started

Launch the API Explorer from your repository root:

```bash
cadr api
```

cadr scans your project files using Tree-sitter AST queries, identifies your framework, extracts all routes, and defaults the target URL to your local dev port (`http://localhost:5000` Flask, `http://localhost:8081` FastAPI, `http://localhost:8080` Gin, `http://localhost:3000` Node/Express). Every URL is configurable — see [Configuration](#configuration).

> `cadr api` also reads `.cadr/cache/endpoints.json` when a previous `cadr run`/`cadr rec` session recorded routes at runtime; that cache takes precedence over static detection.

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
- `Enter`: Start editing the highlighted field (path parameter, query string, headers, or body).
- `Esc`: Stop editing / go back to the endpoint list.
- `b`: Cycle the request body mode (only on methods that accept a body).

### Request Body Modes

The `Body` section carries a single editor whose meaning follows the selected mode:

| Mode | Header shows | Body field | Extra rows |
|---|---|---|---|
| `JSON` (default) | `Body (JSON)` | raw JSON, sent with `application/json` | — |
| `form` | `Body (form)` | one `key=value` per line, sent as `application/x-www-form-urlencoded` | — |
| `multipart` | `Body (multipart)` | optional `key=value` text fields | `File field` (part name) + `File path` |
| `raw` | `Body (raw)` | sent verbatim as `text/plain` | — |

- Body modes are available for `POST`, `PUT`, `PATCH`, `DELETE`, and `OPTIONS`.
- A `Content-Type` header you set yourself always wins over the mode default.
- Leaving `File path` blank in `multipart` sends the text fields only; an unreadable path fails before the request is sent.
- The chosen mode is saved per endpoint in `.cadr/cache/api_cache.json`. Old caches (no mode recorded) default to JSON. There is no body-schema inference: you pick the mode once and it is remembered.

### Request Execution and Caching
- `Ctrl+s`: Send the HTTP request to the target server.
- **Automatic Parameter Persistence:** Every input entered (path variables, query parameters, headers, body payloads, body mode, multipart file field/path) is cached per-endpoint in `.cadr/cache/api_cache.json`. When you return to an endpoint, your previous inputs are restored automatically.
- **Path Parameter Substitution:** Variables in paths (such as `/games/:id` or `/users/{user_id}`) are automatically substituted with the values you input before the HTTP request is dispatched.
- **Failures are not fake responses:** a stopped server or a timeout shows `Status: request failed` with the error; the status code stays empty instead of pretending to be an HTTP 500.
- **Response bodies are capped at 5 MiB** (with a truncation notice) so a runaway response cannot hang the TUI.

### Response Inspection
- `Enter`: Open the response body in your `$EDITOR` (e.g. Neovim) for full-screen analysis or diffing.
- `/`: Open the response search bar to fuzzy-filter text inside large JSON payloads.
- `y`: Copy the raw response body to the system clipboard (`pbcopy` on macOS, `wl-copy`/`xclip` on Linux, `clip` on Windows).

---

## Dynamic Endpoint Caching

Endpoint definitions discovered during scans are cached in `.cadr/cache/endpoints.json`. To force a full re-scan of routes (for instance, after adding new routes), delete this cache file or run `cadr api` with updated source files.

---

## Configuration

`cadr api` reads `.cadr/config.yaml` (falling back to `cadr.yaml`) with a flat `key: value` parser:

```yaml
# .cadr/config.yaml
flask_api_url:   http://localhost:5000
fastapi_api_url: http://localhost:8081
gin_api_url:     http://localhost:8080
node_api_url:    http://localhost:3000
api_url:         http://localhost:9000   # optional: overrides every framework URL
api_timeout:     10s                     # 30s | 30 | 30000ms
```

| Key | Default | Notes |
|---|---|---|
| `flask_api_url` | `http://localhost:5000` | Flask base URL |
| `fastapi_api_url` | `http://localhost:8081` | **8081**, not 8000 |
| `gin_api_url` | `http://localhost:8080` | `PORT` env var sets the default when this key is absent |
| `node_api_url` | `http://localhost:3000` | Express / Fastify / Koa-router |
| `api_url` | — | When set, overrides all four framework URLs above |
| `api_timeout` | `10s` | Accepts a Go duration (`30s`), bare seconds (`30`), or `30000ms`. Invalid values keep 10s and print one warning |

A per-request timeout failure renders `Error: timed out after 30s — raise api_timeout in .cadr/config.yaml`.
