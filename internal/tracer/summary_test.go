package tracer

import "testing"

func TestSummarizeAggregates(t *testing.T) {
	events := []Event{
		ev("main", EvEnter, "1", 1, 0),
		ev("worker", EvEnter, "1", 2, 10),
		ev("", EvExit, "1", 2, 30),
		ev("worker", EvEnter, "1", 3, 40),
		ev("", EvExit, "1", 3, 70),
		ev("", EvExit, "1", 1, 100),
	}
	sum := Summarize(events)
	if sum.EnterCount != 3 || sum.ExitCount != 3 {
		t.Fatalf("enter=%d exit=%d", sum.EnterCount, sum.ExitCount)
	}
	main, ok := sum.FnStatFor("main")
	if !ok {
		t.Fatal("main missing")
	}
	if main.Count != 1 || main.Total != 100 || main.Self != 50 {
		t.Fatalf("main = %+v", main)
	}
	worker, ok := sum.FnStatFor("worker")
	if !ok {
		t.Fatal("worker missing")
	}
	if worker.Count != 2 || worker.Total != 50 || worker.Self != 50 {
		t.Fatalf("worker = %+v", worker)
	}
	if worker.Avg != 25 {
		t.Fatalf("worker avg = %d", worker.Avg)
	}
	if len(sum.Contexts) != 1 || sum.Contexts[0].Root != "main" || sum.Contexts[0].Children != 3 {
		t.Fatalf("contexts = %+v", sum.Contexts)
	}
	if len(sum.Edges) != 1 || sum.Edges[0].Parent != "main" || sum.Edges[0].Child != "worker" || sum.Edges[0].Count != 2 {
		t.Fatalf("edges = %+v", sum.Edges)
	}
}

func TestSummarizeDroppedAndFlat(t *testing.T) {
	// seq gap detection
	events := []Event{
		{Name: "a", Ev: EvEnter, Ctx: "1", Span: 1, Seq: 1, TS: 0},
		{Name: "b", Ev: EvEnter, Ctx: "1", Span: 2, Seq: 2, TS: 1},
		{Name: "c", Ev: EvEnter, Ctx: "1", Span: 3, Seq: 5, TS: 2},
	}
	sum := Summarize(events)
	found := false
	for _, a := range sum.Anomalies {
		if a.Kind == "dropped_events" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected dropped_events anomaly, got %+v", sum.Anomalies)
	}
}

func TestDroppedAnomaliesNamespaced(t *testing.T) {
	nsA := int64(1) << emitterNamespaceBits
	nsB := int64(2) << emitterNamespaceBits
	events := []Event{
		{PID: 1, Seq: nsA + 1},
		{PID: 1, Seq: nsA + 2},
		{PID: 1, Seq: nsA + 4}, // one dropped in namespace A
		{PID: 1, Seq: nsB + 1},
		{PID: 1, Seq: nsB + 2}, // contiguous in namespace B
	}
	anoms := droppedAnomalies(events)
	if len(anoms) != 1 {
		t.Fatalf("anomalies = %+v", anoms)
	}
	if anoms[0].Detail != "1 events missing" {
		t.Fatalf("detail = %q", anoms[0].Detail)
	}
}

func TestDiffFirstDivergence(t *testing.T) {
	a := []Event{ev("main", EvEnter, "1", 1, 0), ev("x", EvEnter, "1", 2, 1), ev("y", EvEnter, "1", 3, 2)}
	b := []Event{ev("main", EvEnter, "1", 1, 0), ev("x", EvEnter, "1", 2, 1), ev("z", EvEnter, "1", 3, 2)}
	res := Diff(a, b)
	if res.FirstDivergence != 2 {
		t.Fatalf("first divergence = %d, want 2", res.FirstDivergence)
	}
	if res.FirstDivergeA == nil || res.FirstDivergeA.Name != "y" {
		t.Fatalf("diverge A = %+v", res.FirstDivergeA)
	}
	if res.FirstDivergeB == nil || res.FirstDivergeB.Name != "z" {
		t.Fatalf("diverge B = %+v", res.FirstDivergeB)
	}
	if len(res.Added) != 1 || res.Added[0] != "z" || len(res.Removed) != 1 || res.Removed[0] != "y" {
		t.Fatalf("added=%v removed=%v", res.Added, res.Removed)
	}

	// Same functions, different order -> order_changed.
	c := []Event{ev("main", EvEnter, "1", 1, 0), ev("x", EvEnter, "1", 2, 1)}
	d := []Event{ev("main", EvEnter, "1", 1, 0), ev("x", EvEnter, "1", 2, 1)}
	if r := Diff(c, d); r.FirstDivergence != -1 {
		t.Fatalf("identical runs diverged at %d", r.FirstDivergence)
	}
	e := []Event{ev("x", EvEnter, "1", 1, 0), ev("main", EvEnter, "1", 2, 1)}
	if r := Diff(c, e); !r.OrderChanged {
		t.Fatalf("expected order_changed, got %+v", r)
	}
}
