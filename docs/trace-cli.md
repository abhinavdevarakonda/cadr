# cadr trace contract (v2)

`cadr trace` and the `trace_*` MCP tools are cadr's **public, versioned** interface
for recorded runs. The out-of-repo pi extension (`pi-cadr`) and any other MCP
client depend on the shapes below. Treat changes as a contract change: bump the
schema version, update this file, and keep old shapes readable.

Schema version: **2** (`tracer.SchemaVersion`, stored in each run's
`<id>.meta.json`). Version 1 streams (no `ev`/`ctx`/`span`/`ts`) remain readable;
they degrade to a flat, enter-only list.

## Run store layout

```
.cadr/traces/runs/<id>.jsonl        # events, one JSON object per line
.cadr/traces/runs/<id>.meta.json    # manifest (schema, cmd, lang, exit_code, ...)
.cadr/traces/runs/<id>.summary.json # cached aggregates
.cadr/traces/last_run.jsonl         # copy of the newest run (backward compat)
```

`<id>` is a sortable UTC timestamp plus a random suffix, e.g.
`20261009T113250.629Z-950446`. Newest runs sort last lexicographically. Retention
keeps the newest 20 runs by default.

## Event schema (v2)

```json
{"lang":"go","fn":"GameByID","file":"/abs/db/game.go","line":412,
 "ev":"enter","ctx":"17","span":42,"seq":7,"pid":9312,"ts":18344210,
 "args":{},"sym":"db.GameByID"}
```

| field | meaning |
|---|---|
| `lang` | `go` \| `python` \| `javascript` |
| `fn`,`file`,`line` | identity (`file` absolute) |
| `ev` | `enter` \| `exit` (absent = legacy enter-only) |
| `ctx` | execution context id (Go goroutine, Python thread:task) |
| `span` | unique invocation id, pairs enter/exit (process-global) |
| `seq` | monotonic counter per emitter instance; gaps mean dropped events |
| `pid` | process id |
| `ts` | monotonic ns since process start (durations only) |
| `args` | optional, enter only |
| `sym` | optional static symbol id |

Line order is execution order. Exits carry only `ev`/`ctx`/`span`/`seq`/`pid`/`ts`;
the matching enter is found by `span`. The tree/summary code in cadr maps
span→enter, so emitters keep no span table.

## CLI

```
cadr trace list    [--json] [--full] [--window N]
cadr trace summary [--run <id|last>] [--json] [--full] [--fn NAME] [--window N] [--graph]
cadr trace tree    [--run <id|last>] [--json] [--ctx ID] [--depth N]
cadr trace diff    <run-a> <run-b> [--json] [--full] [--window N]
cadr trace context [--run <id|last>] [--json] [--ctx ID] [--depth N]
cadr trace verify  [--run <id|last>] [--json] [--strict]
cadr trace clear   [--run <id|last> | --all | --keep N] [--yes] [--json]
```

`--run` defaults to `last`. `--json` emits the stable shapes below; every other
flag only affects the human-readable text. `--full` disables list truncation.
`--graph` (summary) joins the run against the project's static call graph and
adds `dynamic_only` / `static_unused` anomalies.

Exit codes: `0` on success; `1` with a message on stderr when a run is missing
or unreadable. `trace verify` exits `1` when a hard invariant fails (and also on
warnings with `--strict`).

### `trace verify`

`verify` checks the **trace's** internal consistency (not the app's correctness)
and is the gate an agent runs before trusting a run:

| severity | check |
|---|---|
| error | `orphan_exit` — exit with no matching enter |
| error | `duplicate_span` — the same span reused in a context |
| error | `ts_inversion` — exit timestamp before its enter |
| error | `child_time_exceeds_parent` / `child_outside_parent` |
| warn | `open_span` — roots still running (expected for live servers) |
| warn | `incomplete` — enters != exits |
| warn | `dropped_events` — missing `seq` values |
| warn | `missing_ctx` — an event without a context (legacy streams) |

JSON shape: `{ok, errors:[{check,detail,fn,file,line,index}], warnings:[...],
counts:{events,enters,exits,contexts,open_spans,dropped}}`. `ok` is false only
for hard errors; `--strict` makes warnings fatal too.

### `trace clear`

Destructive; confirms first unless `-y`/`--yes` (or the global `-y`) is set.
`--run <id>` deletes one run, `--keep N` retains the newest N, and with no target
flag `--all` (or nothing) deletes every run and `last_run.jsonl`. JSON shape:
`{deleted:[ids], count}`.

### JSON shapes

`trace list --json` → `[]RunMeta`:

```json
[{"schema":2,"id":"...","cmd":"go run .","lang":"go","mode":"full",
  "started":"2026-10-09T11:32:50Z","ended":"...","duration_ms":290,
  "exit_code":0,"dirty":false,"event_count":10,"enter_count":5,
  "exit_count":5,"dropped":0,"complete":true}]
```

`trace summary --json` → `Summary`:

```json
{"schema":2,"event_count":10,"enter_count":5,"exit_count":5,"duration_ns":93970000,
 "functions":[{"fn":"slowChild","file":"/p/main.go","line":8,"count":3,
   "total_ns":93260000,"self_ns":93260000,"avg_ns":31086000,
   "p50_ns":31050000,"p95_ns":31140000,"max_ns":31140000,
   "first_index":2,"last_index":4}],
 "contexts":[{"ctx":"1","root":"main","duration_ns":93970000,"children":5}],
 "edges":[{"parent":"main","child":"orchestrator","count":1}],
 "anomalies":[{"kind":"open_span","detail":"..."}]}
```

`trace tree --json` → `{run, schema, roots:[TreeNode]}` where `TreeNode` is
`{fn,file,line,ctx,dur_ns,self_ns,open,children:[]}`.

`trace diff --json` → `DiffResult`:

```json
{"a":"...","b":"...","added":["newFn"],"removed":["goneFn"],
 "functions":[{"fn":"main","count_a":1,"count_b":1,
   "total_a_ns":100,"total_b_ns":90,"self_a_ns":20,"self_b_ns":18}],
 "first_divergence":7,
 "first_divergence_a":{"fn":"a","file":"/p/f.go","line":3},
 "first_divergence_b":{"fn":"b","file":"/p/f.go","line":9},
 "order_changed":false}
```

`first_divergence` is the index into the **enter** event sequence, or `-1` when
the sequences are identical. `order_changed` is true when the same function
multiset appears in a different order.

`trace context --json` → `{run, schema, contexts:[{ctx,root,duration_ns,spans}]}`.
When `--ctx ID` is given it emits the same `trace tree --json` shape for that
context.

## MCP tools

| tool | args | returns |
|---|---|---|
| `trace_runs` | `limit?` | run ids + manifest one-liners |
| `trace_summary` | `run?`, `fn?`, `full?` | per-function summary (bounded to 25 fns) |
| `trace_tree` | `run?`, `ctx?`, `depth?`, `rows?` | indented call tree |
| `trace_diff` | `a`, `b` | added/removed + deltas + first divergence |
| `trace_context` | `run?` | execution contexts |
| `trace_verify` | `run?` | `ok` + hard errors / warnings for a run |
| `run_trace` | `command` | exit code + summary + first error line |
| `get_last_trace` | `fn?`, `full?`, `limit?` | summary by default; raw JSONL when `full:true` |

All trace tools are bounded by default and point at the run file for the full
data.

## Emitters

- **Go** (`cadr run` / `cadr rec "go run ..."`): cadr writes an overlay that
  rewrites every project `.go` file (adding a `__cadr` import and
  `__cadr.Enter`/`defer __cadr.Exit`) and points the build at a single generated
  runtime module (`cadr.internal/runtime`, wired in through an alternate
  `-modfile`). Because one runtime package is imported by every package, there
  is **one** socket sender, span counter and seq counter for the whole process:
  spans are globally unique and cross-package order is exact. `ctx` is the
  goroutine id parsed from `runtime.Stack`. `CADR_TRACE_MODE=light` keeps the
  single legacy call (flat, no durations). The instrumented `main` registers a
  drain barrier (`defer __cadr.Flush()`) so the root exit is flushed before the
  process exits. Nothing is written into the user's module: the runtime module,
  alternate modfile and instrumented copies all live under `.cadr/cache/`.
  Long-running roots (`main`, background goroutines) still report open spans
  when the recording stops.
- **Python** (`sitecustomize` + `sys.settrace`): `call` emits enter and installs
  a per-frame tracer that emits exit on `return`. `ctx` is `thread_id:task_id`.
  An `atexit` barrier drains the sender before the interpreter exits.
