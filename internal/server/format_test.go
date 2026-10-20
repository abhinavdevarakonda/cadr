package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abhinavdevarakonda/cadr/internal/frameworks"
	"github.com/abhinavdevarakonda/cadr/internal/tracer"
)

// writeRun records a run in a temp root and returns the loaded copy.
func writeRun(t *testing.T, events []tracer.Event, exitCode int) (string, *tracer.TraceRun) {
	t.Helper()
	root := t.TempDir()
	store := tracer.NewRunStore(root)
	w, err := store.Create("go run .", "go", "full")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, e := range events {
		if err := w.WriteEvent(e); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := w.Close(exitCode); err != nil {
		t.Fatalf("close: %v", err)
	}
	run, err := store.Load("last")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return root, run
}

// requestEvents builds one context: ListBooks -> QueryBooks.
func requestEvents(ctx string, baseSpan, baseTS int64) []tracer.Event {
	return []tracer.Event{
		{Name: "handlers.ListBooks", File: "/app/handlers.go", Line: 42, Ev: tracer.EvEnter, Ctx: ctx, Span: baseSpan, Seq: baseSpan},
		{Name: "store.QueryBooks", File: "/app/store.go", Line: 22, Ev: tracer.EvEnter, Ctx: ctx, Span: baseSpan + 1, Seq: baseSpan + 1, TS: baseTS + 10},
		{Name: "store.QueryBooks", Ev: tracer.EvExit, Ctx: ctx, Span: baseSpan + 1, Seq: baseSpan + 2, TS: baseTS + 60},
		{Name: "handlers.ListBooks", Ev: tracer.EvExit, Ctx: ctx, Span: baseSpan, Seq: baseSpan + 3, TS: baseTS + 100},
	}
}

// writeRoutes records the discovery cache so contexts can be labelled.
func writeRoutes(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".cadr", "cache")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	eps := []frameworks.Endpoint{{Method: "GET", Path: "/books", HandlerFunc: "ListBooks", Framework: "gin"}}
	data, _ := json.Marshal(eps)
	if err := os.WriteFile(filepath.Join(dir, "endpoints.json"), data, 0644); err != nil {
		t.Fatalf("write routes: %v", err)
	}
}

// A server run lists one line per request, hottest first, labelled with its route.
func TestFormatContextsListsRequests(t *testing.T) {
	events := append(requestEvents("1", 1, 0), requestEvents("2", 10, 1000)...)
	root, run := writeRun(t, events, 0)
	writeRoutes(t, root)

	entries, idle := ctxEntries(tracer.BuildForest(run.Events), root)
	out := formatContexts(run, root, entries, idle, 20)
	if !strings.Contains(out, "GET /books") {
		t.Fatalf("route label missing:\n%s", out)
	}
	if !strings.Contains(out, "ctx=1") || !strings.Contains(out, "ctx=2") {
		t.Fatalf("contexts missing:\n%s", out)
	}
	if !strings.Contains(out, "2 context(s)") {
		t.Fatalf("context count missing:\n%s", out)
	}
	if !strings.Contains(out, "trace_tree") {
		t.Fatalf("missing next-step hint:\n%s", out)
	}
}

// An unscoped tree on a busy run must not dump thousands of merged rows.
func TestFormatTreeRefusesToDumpManyContexts(t *testing.T) {
	var events []tracer.Event
	for i := 1; i <= 4; i++ {
		events = append(events, requestEvents(string(rune('0'+i)), int64(i*10), int64(i*1000))...)
	}
	root, run := writeRun(t, events, 0)
	forest := tracer.BuildForest(run.Events)

	out := formatTree(run, forest, root, treeScope{}, -1, 200)
	if !strings.Contains(out, "4 contexts") {
		t.Fatalf("expected a refusal that names the context count:\n%s", out)
	}
	if strings.Contains(out, "store.QueryBooks") {
		t.Fatalf("must not render rows for an unscoped busy run:\n%s", out)
	}
}

// One context renders exactly that request, with its source locations.
func TestFormatTreeScopedToContext(t *testing.T) {
	events := append(requestEvents("1", 1, 0), requestEvents("2", 10, 1000)...)
	root, run := writeRun(t, events, 0)

	out := formatTree(run, tracer.BuildForest(run.Events), root, treeScope{Ctx: "2"}, -1, 200)
	if !strings.Contains(out, "ctx=2") {
		t.Fatalf("missing ctx header:\n%s", out)
	}
	if strings.Contains(out, "ctx=1") {
		t.Fatalf("leaked another context:\n%s", out)
	}
	if !strings.Contains(out, "store.QueryBooks") {
		t.Fatalf("missing child call:\n%s", out)
	}
}

// The route view merges every request to that route into one tree with counts.
func TestFormatRouteTreeMergesRequests(t *testing.T) {
	events := append(requestEvents("1", 1, 0), requestEvents("2", 10, 1000)...)
	root, run := writeRun(t, events, 0)
	writeRoutes(t, root)
	forest := tracer.BuildForest(run.Events)

	out := formatTree(run, forest, root, treeScope{Route: "GET /books"}, -1, 200)
	if !strings.Contains(out, "2 request(s)") {
		t.Fatalf("expected two merged requests:\n%s", out)
	}
	if !strings.Contains(out, "×2") {
		t.Fatalf("expected call counts on the merged edges:\n%s", out)
	}
}

// Truncation must state what was shown and how to see more, never just the total.
func TestTruncationNote(t *testing.T) {
	if got := truncationNote(5, 5, 40); got != "" {
		t.Fatalf("expected no note when everything was shown, got %q", got)
	}
	got := truncationNote(5, 40, 5)
	if !strings.Contains(got, "showing 5 of 40") || !strings.Contains(got, "rows=N") {
		t.Fatalf("unhelpful truncation note: %q", got)
	}
}

// A function scope shows that function's subtree wherever it ran.
func TestFormatTreeScopedToFunction(t *testing.T) {
	events := append(requestEvents("1", 1, 0), requestEvents("2", 10, 1000)...)
	root, run := writeRun(t, events, 0)

	out := formatTree(run, tracer.BuildForest(run.Events), root, treeScope{Fn: "store.QueryBooks"}, -1, 200)
	if !strings.Contains(out, "2 occurrence(s)") {
		t.Fatalf("expected both occurrences:\n%s", out)
	}
	if strings.Contains(out, "handlers.ListBooks") {
		t.Fatalf("subtree must start at the requested function:\n%s", out)
	}
}
