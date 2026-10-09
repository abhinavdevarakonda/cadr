package tracer

import "testing"

func TestVerifyCleanRun(t *testing.T) {
	events := []Event{
		ev("main", EvEnter, "1", 1, 0),
		ev("child", EvEnter, "1", 2, 1),
		ev("", EvExit, "1", 2, 5),
		ev("", EvExit, "1", 1, 9),
	}
	rep := Verify(events)
	if !rep.OK || len(rep.Errors) != 0 || len(rep.Warnings) != 0 {
		t.Fatalf("clean run reported problems: %+v", rep)
	}
	if rep.Counts.Enters != 2 || rep.Counts.Exits != 2 || rep.Counts.Contexts != 1 {
		t.Fatalf("counts = %+v", rep.Counts)
	}
}

func TestVerifyOrphanExitAndDrops(t *testing.T) {
	events := []Event{
		{Name: "a", Ev: EvEnter, Ctx: "1", Span: 1, Seq: 1, TS: 0},
		{Ev: EvExit, Ctx: "1", Span: 99, Seq: 2, TS: 1}, // orphan exit
		{Name: "b", Ev: EvEnter, Ctx: "1", Span: 2, Seq: 4, TS: 2},
	}
	rep := Verify(events)
	if rep.OK {
		t.Fatal("expected a hard error")
	}
	found := false
	for _, e := range rep.Errors {
		if e.Check == "orphan_exit" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no orphan_exit in %+v", rep.Errors)
	}
	if rep.Counts.Dropped != 1 {
		t.Fatalf("dropped = %d, want 1", rep.Counts.Dropped)
	}
	if len(rep.Warnings) == 0 {
		t.Fatalf("expected seq-gap warning")
	}
}

func TestVerifyOpenSpansAreWarnings(t *testing.T) {
	rep := Verify([]Event{ev("main", EvEnter, "1", 1, 0)})
	if !rep.OK {
		t.Fatalf("open span must be a warning, not an error: %+v", rep.Errors)
	}
	if rep.Counts.OpenSpans != 1 {
		t.Fatalf("open = %d, want 1", rep.Counts.OpenSpans)
	}
	if len(rep.Warnings) == 0 {
		t.Fatalf("expected open_span/incomplete warnings")
	}
}

func TestVerifyChildTimeExceedsParent(t *testing.T) {
	events := []Event{
		ev("parent", EvEnter, "1", 1, 0),
		ev("child", EvEnter, "1", 2, 10),
		ev("", EvExit, "1", 2, 100), // child 90ns
		ev("", EvExit, "1", 1, 50),  // parent 50ns < child
	}
	rep := Verify(events)
	if rep.OK {
		t.Fatal("expected child_time_exceeds_parent")
	}
	found := false
	for _, e := range rep.Errors {
		if e.Check == "child_time_exceeds_parent" {
			found = true
		}
	}
	if !found {
		t.Fatalf("errors = %+v", rep.Errors)
	}
}

func TestVerifyLegacyNoIncompleteWarning(t *testing.T) {
	events := []Event{
		{Name: "a", File: "/p/a.go", Line: 1},
		{Name: "b", File: "/p/a.go", Line: 2},
	}
	rep := Verify(events)
	if !rep.OK {
		t.Fatalf("legacy stream should pass: %+v", rep.Errors)
	}
	for _, w := range rep.Warnings {
		if w.Check == "incomplete" {
			t.Fatalf("legacy stream must not warn incomplete: %+v", rep.Warnings)
		}
	}
}

func TestVerifyDuplicateSpan(t *testing.T) {
	events := []Event{
		ev("a", EvEnter, "1", 7, 0),
		ev("b", EvEnter, "1", 7, 1), // same span in same ctx
	}
	rep := Verify(events)
	if rep.OK {
		t.Fatal("expected duplicate_span error")
	}
}
