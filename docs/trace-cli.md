# cadr trace: Command-Line Trace Analysis

`cadr trace` provides offline queries, diffing, call trees, and verification for recorded execution traces stored under `.cadr/traces/`.

Every command operates on a recorded run (default: the most recent run) and supports both terminal-formatted output and raw `--json` output for scripting, CI pipelines, and agent integration.

---

## Choosing a subcommand

Runs are produced by `cadr rec "<command>"` (timebox by wrapping cadr: `timeout 60s cadr rec "..."`).
Everything below only *reads* runs. `last` is always a valid `--run` value, so `trace list` is often
skippable.

```
cadr rec "<cmd>"  →  cadr trace list  →  summary  →  context  →  tree  →  diff | verify
                                     (digest)     (pick a request)  (drill)
```

| You want to know | Command | What you get |
| --- | --- | --- |
| What runs do I have? | `cadr trace list` | id, start time, duration, exit code, event count, command |
| Did it fail, and where? | `cadr trace summary` | `exit_code`, an `errors:` block with `file:line`, hottest functions |
| Which request was slow? | `cadr trace tree` (no `--ctx`) | lists the contexts, hottest first |
| What did that request do, in order? | `cadr trace tree --ctx <id>` | indented tree with per-function total + self time |
| How slow is one function? | `cadr trace summary --fn <name>` | count / total / self / p95 / max |
| Did my change alter behaviour? | `cadr trace diff <before> <after>` | added/removed functions, deltas, first divergence |
| Can I trust this trace? | `cadr trace verify` | span pairing, seq integrity, open spans |
| What does my change touch? | `cadr trace summary --graph` | plus `dynamic_only` / `static_unused` graph join |
| Clean up old runs | `cadr trace clear --keep <n>` | keeps the newest n runs |

Two caveats worth knowing:

- `--fn` works for both: `summary --fn X` gives that function's stats, `tree --fn X` renders
  that function's subtree (and errors if it never ran in the run).
- The merged per-route view (`×N` call counts collapsed across requests) is available through the
  MCP `trace_tree(route=...)` tool, not the CLI. The CLI is limited to `--ctx` and `--depth`.

---

## Storage Layout

Runs are recorded via `cadr rec <command>` and stored in `.cadr/traces/`:

```text
.cadr/traces/runs/
  <id>.jsonl          # raw trace events (newline-delimited JSON)
  <id>.meta.json      # manifest: cmd, exit code, duration, counts, error count
  <id>.summary.json   # per-function / per-context / per-edge aggregates
  <id>.errors.json    # crash records recovered from the run's stderr (when present)
  <id>.stderr.log     # bounded stderr tail (when the process wrote any)
```

Each run has a sortable timestamped identifier (e.g. `20261014T153301.412Z-9a4f5f`).
`runs/` is the single source of truth for "the last run": there is no `last_run.jsonl` any
more. A pre-v3 `last_run.jsonl` is adopted once as an orphan run so old traces stay visible,
and `cadr trace clear` removes it.

### Errors

A panic or uncaught exception is recovered from the stderr of the recorded process, localized
to a `file:line` (Go panic goroutine stacks, `net/http`-recovered handler panics, and Python
tracebacks are all parsed), and written to `<id>.errors.json`. A handled error that leaves the
process running is not a crash record; a panic that `net/http` recovers is, even though the run
then exits 0. Stopping a server with Ctrl-C still records the crash seen just before it.
In the two-process workflow (`cadr .` for the TUI, `cadr run "<cmd>"` for the app),
`cadr run` forwards each crash to the listening TUI over the trace socket, so the TUI
marks the failing function too.
Localization takes the innermost frame that lives inside the project and marks the function whose
source range contains that line; paths are compared tolerantly because the graph stores
project-relative paths while a stack reports absolute ones. Frames in libraries or at module level
resolve to no function, so they mark nothing rather than guessing a caller. `meta.json` also carries `error_count` and `first_error` so a failed run
is identifiable from `cadr trace list`. `trace summary` and `trace verify` print an `errors:`
section. `complete` means "the event stream is internally consistent", not "the run succeeded" —
always read it next to `exit_code` and `error_count`.

---

## Subcommands

### `cadr trace list`
Lists all recorded runs with their IDs, timestamps, target commands, duration, and event counts.

```bash
# human-readable table
cadr trace list

# output raw JSON
cadr trace list --json
```

---

### `cadr trace summary`
Aggregates function execution metrics for a run, including total invocations, total execution time, and self time (excluding child calls).

```bash
# summarize the most recent run
cadr trace summary

# summarize a specific run
cadr trace summary --run <run_id>

# filter by a specific function name
cadr trace summary --fn JoinQueue

# cross-reference with the static call graph
cadr trace summary --graph

# output raw JSON
cadr trace summary --json
```

Flags:
- `--run <id|last>`: Target run identifier (default: `last`).
- `--fn <name>`: One function — `summary` shows its stats, `tree` shows its subtree.
- `--graph`: Join runtime metrics against the static dependency graph to show untraced/unreached callees.
- `--window <n>`: Limit visible rows before truncation (default: `20`).
- `--full`: Display all rows without truncation.
- `--json`: Machine-readable output.

---

### `cadr trace tree`
Renders the hierarchical call tree of a run, showing execution nesting, child calls, and per-span durations.

```bash
# render call tree of the last run
cadr trace tree

# limit tree depth
cadr trace tree --depth 3

# filter to a specific execution context / thread
cadr trace tree --ctx <context_id>

# output raw JSON
cadr trace tree --json
```

Flags:
- `--run <id|last>`: Target run identifier.
- `--depth <n>`: Maximum tree recursion depth (default: unlimited).
- `--ctx <id>`: Filter to a specific async context, thread, or process ID.
- `--json`: Emit hierarchical JSON nodes with `span_id`, `parent_span_id`, `duration_ms`, and `children`.

---

### `cadr trace diff`
Compares two execution runs and reports structural and performance differences: added calls, removed calls, invocation count deltas, and latency regressions.

```bash
# compare the last run against the second-to-last run
cadr trace diff

# compare two explicit runs
cadr trace diff <run_id_a> <run_id_b>

# output machine-readable JSON diff
cadr trace diff --json
```

Output highlights:
- `+`: Functions called in run B that were not called in run A.
- `-`: Functions called in run A that were absent in run B.
- `~`: Functions with invocation count or execution time shifts.

---

### `cadr trace verify`
Validates that a recorded trace conforms to expected constraints or matches the static AST graph. Ideal for automated testing and CI verification.

```bash
# verify the last run
cadr trace verify

# treat any warning or unexpected divergence as a failure
cadr trace verify --strict

# cross-reference every executed node against the static graph
cadr trace verify --graph
```

Exit codes:
- `0`: Verification passed.
- `1`: Inconsistencies detected (e.g. unbalanced spans, orphan events, or unresolvable call sites under `--strict`).

---

### `cadr trace clear`
Purges recorded trace files from `.cadr/traces/`.

```bash
# clear all traces (prompts for confirmation)
cadr trace clear

# keep only the newest 5 runs
cadr trace clear --keep 5

# clear all without confirmation prompt
cadr trace clear --all --yes
```

---

## Machine-Readable JSON Schemas

When `--json` is supplied, commands emit structured JSON to stdout.

### Summary JSON Shape (`cadr trace summary --json`)
```json
[
  {
    "function": "handlers.JoinQueue",
    "file": "handlers/queue.go",
    "line": 42,
    "calls": 12,
    "total_time_ms": 145.2,
    "self_time_ms": 12.8,
    "avg_time_ms": 12.1
  }
]
```

### Tree JSON Shape (`cadr trace tree --json`)
```json
[
  {
    "span_id": 101,
    "name": "handlers.JoinQueue",
    "file": "handlers/queue.go",
    "line": 42,
    "duration_ms": 145.2,
    "children": [
      {
        "span_id": 102,
        "name": "handlers.fetchGameState",
        "file": "handlers/queue.go",
        "line": 85,
        "duration_ms": 132.4,
        "children": []
      }
    ]
  }
]
```
