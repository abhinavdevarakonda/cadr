package tracer

import "testing"

// Only enter/exit lines are trace events. Anything else must be treated as
// diagnostic output: a line with an unknown event kind would otherwise be counted
// as an enter and inserted as a phantom node (see Summarize, BuildForest).
func TestParseEventLineClassification(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"legacy enter", `{"fn":"main","file":"a.go","line":1}`, true},
		{"enter", `{"fn":"main","file":"a.go","line":1,"ev":"enter"}`, true},
		{"exit", `{"fn":"main","file":"a.go","line":1,"ev":"exit","span":1}`, true},
		// Go emits exits without fn; they are identified by their span.
		{"exit without fn", `{"ev":"exit","ctx":"1","span":2,"seq":4,"pid":7,"ts":53}`, true},
		{"enter without fn", `{"ev":"enter","ctx":"1","span":2}`, false},
		{"unknown ev", `{"fn":"main","file":"a.go","line":1,"ev":"error"}`, false},
		{"no fn", `{"ev":"enter","line":1}`, false},
		{"not json", `panic: boom`, false},
		{"plain number", `42`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := ParseEventLine([]byte(tc.line)); ok != tc.want {
				t.Fatalf("ParseEventLine(%q) ok = %v, want %v", tc.line, ok, tc.want)
			}
		})
	}
}

// A dropped line must not affect the run's counts or its tree.
func TestUnknownEventDoesNotAffectSummary(t *testing.T) {
	events := []Event{
		{Name: "main", File: "a.go", Line: 1, Ev: EvEnter, Ctx: "1", Span: 1, Seq: 1},
		{Name: "main", File: "a.go", Line: 1, Ev: EvExit, Ctx: "1", Span: 1, Seq: 2},
	}
	sum := Summarize(events)
	if sum.EnterCount != 1 || sum.ExitCount != 1 {
		t.Fatalf("counts = enter %d exit %d, want 1/1", sum.EnterCount, sum.ExitCount)
	}
}
