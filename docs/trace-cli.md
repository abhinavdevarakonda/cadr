# cadr trace: Command-Line Trace Analysis

`cadr trace` provides offline queries, diffing, call trees, and verification for recorded execution traces stored under `.cadr/traces/`.

Every command operates on a recorded run (default: the most recent run) and supports both terminal-formatted output and raw `--json` output for scripting, CI pipelines, and agent integration.

---

## Storage Layout

Runs are recorded via `cadr rec <command>` and stored in `.cadr/traces/`:

```text
.cadr/traces/
  runs.json                # run index and metadata catalog
  run_<id>.jsonl           # raw trace events (newline-delimited JSON)
```

Each run has a timestamped identifier (e.g., `run_20261005_120000`).

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
- `--fn <name>`: Filter rows by function name.
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

### `cadr trace context`
Lists execution contexts (threads, goroutines, or async task IDs) present in a run.

```bash
cadr trace context
cadr trace context --run <run_id>
cadr trace context --json
```

Useful for debugging multi-threaded Python workloads, concurrent Go goroutines, or asynchronous Node.js task flows.

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
