package tracer

import "testing"

func ev(fn, kind, ctx string, span, ts int64) Event {
	return Event{Name: fn, File: "/p/f.go", Line: 1, Ev: kind, Ctx: ctx, Span: span, TS: ts}
}

func TestBuildForestNestingAndDurations(t *testing.T) {
	events := []Event{
		ev("main", EvEnter, "1", 1, 0),
		ev("helper", EvEnter, "1", 2, 10),
		ev("", EvExit, "1", 2, 30),
		ev("", EvExit, "1", 1, 100),
	}
	f := BuildForest(events)
	if len(f.Flat) != 2 {
		t.Fatalf("flat rows = %d, want 2", len(f.Flat))
	}
	if f.Flat[0].Depth != 0 || f.Flat[1].Depth != 1 {
		t.Fatalf("depths = %d,%d want 0,1", f.Flat[0].Depth, f.Flat[1].Depth)
	}
	if !f.Flat[0].CtxRoot || f.Flat[1].CtxRoot {
		t.Fatalf("ctx flags wrong: %+v", f.Flat)
	}
	main := f.Flat[0].Node
	helper := f.Flat[1].Node
	if main.Dur != 100 || main.Self != 80 {
		t.Fatalf("main dur=%d self=%d want 100/80", main.Dur, main.Self)
	}
	if helper.Dur != 20 || helper.Self != 20 {
		t.Fatalf("helper dur=%d self=%d want 20/20", helper.Dur, helper.Self)
	}
	if !f.Flat[1].Last {
		t.Fatalf("child should be last")
	}
	if len(f.OpenSpans()) != 0 {
		t.Fatalf("unexpected open spans: %+v", f.OpenSpans())
	}
}

func TestBuildForestContexts(t *testing.T) {
	events := []Event{
		ev("root1", EvEnter, "1", 1, 0),
		ev("child", EvEnter, "1", 2, 5),
		ev("root2", EvEnter, "2", 3, 6),
		ev("", EvExit, "1", 2, 8),
		ev("", EvExit, "1", 1, 9),
		ev("", EvExit, "2", 3, 10),
	}
	f := BuildForest(events)
	if len(f.Contexts) != 2 {
		t.Fatalf("contexts = %d, want 2", len(f.Contexts))
	}
	// ctx 2's root must not nest under ctx 1's still-open root.
	var ctx2Depth int
	for _, row := range f.Flat {
		if row.Ctx == "2" {
			ctx2Depth = row.Depth
		}
	}
	if ctx2Depth != 0 {
		t.Fatalf("ctx 2 root depth = %d, want 0", ctx2Depth)
	}
}

func TestBuildForestUnbalanced(t *testing.T) {
	events := []Event{
		ev("open", EvEnter, "1", 1, 0),
		ev("", EvExit, "1", 99, 5), // orphan exit, ignored
		ev("", EvExit, "1", 1, 7),
	}
	f := BuildForest(events)
	if len(f.Flat) != 1 {
		t.Fatalf("flat rows = %d", len(f.Flat))
	}
	if f.Flat[0].Node.Dur != 7 || f.Flat[0].Node.Open {
		t.Fatalf("node = %+v", f.Flat[0].Node)
	}

	events = []Event{
		ev("opened", EvEnter, "1", 1, 0),
		ev("closed", EvEnter, "1", 2, 1),
		ev("", EvExit, "1", 2, 5),
	}
	f = BuildForest(events)
	if len(f.OpenSpans()) != 1 {
		t.Fatalf("want 1 open span, got %d", len(f.OpenSpans()))
	}
}

func TestBuildForestFlatLegacy(t *testing.T) {
	// No exits anywhere: legacy v1 / mode:light must degrade to depth 0.
	events := []Event{
		{Name: "a", File: "/p/a.go", Line: 1},
		{Name: "b", File: "/p/a.go", Line: 2},
		{Name: "a", File: "/p/a.go", Line: 1},
	}
	f := BuildForest(events)
	for i, row := range f.Flat {
		if row.Depth != 0 {
			t.Fatalf("row %d depth = %d, want 0", i, row.Depth)
		}
		if row.CtxRoot != (i == 0) {
			t.Fatalf("row %d CtxRoot = %v", i, row.CtxRoot)
		}
		if row.Node.Open {
			t.Fatalf("legacy nodes must not be flagged open")
		}
	}
	if len(f.OpenSpans()) != 0 {
		t.Fatalf("legacy stream must not report open spans")
	}
}

func TestNegativeSelfClamped(t *testing.T) {
	// Parent exits before its child: self-time must not go negative.
	events := []Event{
		ev("p", EvEnter, "1", 1, 0),
		ev("c", EvEnter, "1", 2, 1),
		ev("", EvExit, "1", 1, 2),
		ev("", EvExit, "1", 2, 10),
	}
	f := BuildForest(events)
	if f.Flat[0].Node.Self < 0 {
		t.Fatalf("self = %d, want >= 0", f.Flat[0].Node.Self)
	}
}
