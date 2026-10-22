package server

import (
	"time"

	"github.com/abhinavdevarakonda/cadr/internal/tracer"
)

// traceEnvelope is the structured shape every trace tool returns alongside its text
// rendering, so a client never has to parse whitespace-padded columns. Durations are
// nanoseconds, matching the run store's own JSON shapes.
type traceEnvelope struct {
	RunID    string              `json:"run_id"`
	Cmd      string              `json:"cmd"`
	ExitCode int                 `json:"exit_code"`
	Complete bool                `json:"complete"`
	WallNs   int64               `json:"wall_ns"`
	TraceNs  int64               `json:"trace_ns"`
	Events   int                 `json:"events"`
	Enter    int                 `json:"enter"`
	Exit     int                 `json:"exit"`
	Errors   []tracer.TraceError `json:"errors,omitempty"`
	FullRun  string              `json:"full_run_file,omitempty"`
}

// newEnvelope fills the common header for a run.
func newEnvelope(run *tracer.TraceRun) traceEnvelope {
	env := traceEnvelope{
		RunID:    run.ID,
		Cmd:      run.Meta.Cmd,
		ExitCode: run.Meta.ExitCode,
		Complete: run.Meta.Complete,
		WallNs:   run.Meta.DurationMs * int64(time.Millisecond),
		Errors:   run.Errors,
		FullRun:  runFilePath(run),
	}
	if run.Summary != nil {
		env.TraceNs = run.Summary.DurationNs
		env.Events = run.Summary.EventCount
		env.Enter = run.Summary.EnterCount
		env.Exit = run.Summary.ExitCount
	}
	return env
}

// summaryOut is the structured form of trace_summary / get_last_trace.
type summaryOut struct {
	traceEnvelope
	Functions []tracer.FnStat  `json:"functions"`
	Contexts  int              `json:"contexts"`
	Warnings  []tracer.Anomaly `json:"warnings,omitempty"`
	Anomalies []tracer.Anomaly `json:"anomalies,omitempty"`
}

// treeScopeOut names the slice a trace_tree result covers, so a client can tell a
// scoped answer from a full-run dump.
type treeScopeOut struct {
	Kind  string `json:"kind"` // ctx | fn | route | all
	Ctx   string `json:"ctx,omitempty"`
	Fn    string `json:"fn,omitempty"`
	Route string `json:"route,omitempty"`
}

// treeOut is the structured form of trace_tree. The call tree itself stays in the
// text rendering: indentation is the information, so there is nothing to parse.
type treeOut struct {
	traceEnvelope
	Scope treeScopeOut `json:"scope"`
}

// verifyOut is the structured form of trace_verify.
type verifyOut struct {
	traceEnvelope
	OK         bool                `json:"ok"`
	Counts     tracer.VerifyCounts `json:"counts"`
	Violations []tracer.Violation  `json:"violations,omitempty"`
	Warnings   []tracer.Violation  `json:"warnings,omitempty"`
}

// diffOut is the structured form of trace_diff. It describes two runs, so it does
// not embed traceEnvelope.
type diffOut struct {
	A     string             `json:"a"`
	B     string             `json:"b"`
	Diff  *tracer.DiffResult `json:"diff"`
	ARuns string             `json:"a_run_file,omitempty"`
	BRuns string             `json:"b_run_file,omitempty"`
}

// scopeOf names the slice a treeScope selects.
func scopeOf(s treeScope) treeScopeOut {
	switch {
	case s.Route != "":
		return treeScopeOut{Kind: "route", Route: s.Route}
	case s.Fn != "":
		return treeScopeOut{Kind: "fn", Fn: s.Fn}
	case s.Ctx != "":
		return treeScopeOut{Kind: "ctx", Ctx: s.Ctx}
	}
	return treeScopeOut{Kind: "all"}
}
