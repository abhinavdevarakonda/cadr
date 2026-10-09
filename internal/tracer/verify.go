package tracer

import "fmt"

// maxViolations bounds the report so a badly broken trace cannot flood a
// caller. The counts stay exact; the detail lists are truncated.
const maxViolations = 50

// Violation is one failed (or warned) invariant.
type Violation struct {
	Check  string `json:"check"`
	Detail string `json:"detail,omitempty"`
	Fn     string `json:"fn,omitempty"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Index  int    `json:"index,omitempty"`
}

// VerifyCounts summarizes the run the report was computed from.
type VerifyCounts struct {
	Events    int `json:"events"`
	Enters    int `json:"enters"`
	Exits     int `json:"exits"`
	Contexts  int `json:"contexts"`
	OpenSpans int `json:"open_spans"`
	Dropped   int `json:"dropped"`
}

// VerifyReport is the result of Verify. OK is false when any hard invariant is
// violated; warnings (open spans, incomplete runs) do not by themselves make it
// false.
type VerifyReport struct {
	OK       bool         `json:"ok"`
	Errors   []Violation  `json:"errors"`
	Warnings []Violation  `json:"warnings"`
	Counts   VerifyCounts `json:"counts"`
}

func (r *VerifyReport) addError(v Violation) {
	r.OK = false
	if len(r.Errors) < maxViolations {
		r.Errors = append(r.Errors, v)
	}
}

func (r *VerifyReport) addWarning(v Violation) {
	if len(r.Warnings) < maxViolations {
		r.Warnings = append(r.Warnings, v)
	}
}

// Verify checks the internal consistency of an event stream: span pairing,
// timestamp ordering, duration/child consistency, context presence and seq
// integrity. It validates the trace, not the application (a well-formed trace
// can still describe a buggy program). Open spans and an enter/exit mismatch are
// warnings because a live server legitimately leaves roots running.
func Verify(events []Event) *VerifyReport {
	rep := &VerifyReport{OK: true, Errors: []Violation{}, Warnings: []Violation{}}
	forest := BuildForest(events)
	rep.Counts.Events = len(events)
	rep.Counts.Contexts = len(forest.Contexts)

	anyCtx := false
	hasSpan := false
	for _, e := range events {
		if NormalizeEvent(e).Ctx != "" {
			anyCtx = true
		}
		if NormalizeEvent(e).Span > 0 {
			hasSpan = true
		}
	}

	type spanKey struct {
		ctx  string
		span int64
	}
	open := make(map[spanKey]Event)
	seen := make(map[spanKey]Event)
	for i, raw := range events {
		e := NormalizeEvent(raw)
		if e.Ev == EvExit {
			rep.Counts.Exits++
			k := spanKey{e.Ctx, e.Span}
			ent, ok := open[k]
			if !ok {
				rep.addError(Violation{Check: "orphan_exit", Detail: "exit has no matching enter", Index: i})
				continue
			}
			if e.TS < ent.TS {
				rep.addError(Violation{Check: "ts_inversion", Detail: fmt.Sprintf("exit ts %d < enter ts %d", e.TS, ent.TS), Fn: ent.Name, File: ent.File, Line: ent.Line, Index: i})
			}
			delete(open, k)
			continue
		}
		rep.Counts.Enters++
		if anyCtx && e.Ctx == "" {
			rep.addWarning(Violation{Check: "missing_ctx", Fn: e.Name, File: e.File, Line: e.Line, Index: i})
		}
		if e.Span > 0 {
			k := spanKey{e.Ctx, e.Span}
			if prev, dup := seen[k]; dup {
				rep.addError(Violation{
					Check:  "duplicate_span",
					Detail: fmt.Sprintf("span %d in ctx %q reused by %s and %s", e.Span, e.Ctx, prev.Name, e.Name),
					Fn:     e.Name, File: e.File, Line: e.Line, Index: i,
				})
			}
			seen[k] = e
			open[k] = e
		}
	}
	rep.Counts.OpenSpans = len(open)
	openByFn := make(map[string]int)
	for _, ent := range open {
		openByFn[ent.Name]++
	}
	for fn, n := range openByFn {
		rep.addWarning(Violation{Check: "open_span", Detail: fmt.Sprintf("%s x%d", fn, n), Fn: fn})
	}

	forest.Walk(func(node *TreeNode) {
		var childSum int64
		for _, c := range node.Children {
			childSum += c.Dur
			if !node.Open && node.End > 0 && c.End > node.End {
				rep.addError(Violation{Check: "child_outside_parent", Detail: c.Event.Name, Fn: c.Event.Name, File: c.Event.File, Line: c.Event.Line})
			}
		}
		if childSum > node.Dur {
			rep.addError(Violation{Check: "child_time_exceeds_parent", Detail: fmt.Sprintf("children %dns > parent %dns", childSum, node.Dur), Fn: node.Event.Name, File: node.Event.File, Line: node.Event.Line})
		}
	})

	if d := droppedCount(events); d > 0 {
		rep.Counts.Dropped = d
		rep.addWarning(Violation{Check: "dropped_events", Detail: fmt.Sprintf("%d events missing", d)})
	}
	if rep.Counts.Enters != rep.Counts.Exits && hasSpan {
		rep.addWarning(Violation{Check: "incomplete", Detail: fmt.Sprintf("%d enters, %d exits", rep.Counts.Enters, rep.Counts.Exits)})
	}
	return rep
}
