package server

import (
	"strings"
	"testing"

	"github.com/abhinavdevarakonda/cadr/internal/tracer"
	mcp "github.com/mark3labs/mcp-go/mcp"
)

// resultText extracts the human-readable text from a tool result.
func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatalf("empty tool result")
	}
	tc, ok := res.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want TextContent", res.Content[0])
	}
	return tc.Text
}

// recordRun writes one run with a crash to a temp project root.
func recordRun(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	store := tracer.NewRunStore(root)
	w, err := store.Create("go run .", "go", "full")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := w.WriteEvent(tracer.Event{Name: "main", File: "/app/main.go", Line: 1, Ev: tracer.EvEnter, Ctx: "1", Span: 1, Seq: 1}); err != nil {
		t.Fatalf("write: %v", err)
	}
	d := tracer.NewDiagnostics()
	for _, line := range []string{
		"panic: runtime error: index out of range [7] with length 3",
		"goroutine 1 [running]:",
		"main.crash(...)",
		"\t/app/main.go:5",
	} {
		d.Add(line)
	}
	w.SetDiagnostics(d)
	if err := w.Close(2); err != nil {
		t.Fatalf("close: %v", err)
	}
	return root
}

// trace_summary must report the crash recovered from stderr, not just the table.
func TestSummaryResultReportsErrors(t *testing.T) {
	root := recordRun(t)
	res, err := summaryResult(root, nil, "last", "", false)
	if err != nil {
		t.Fatalf("summaryResult: %v", err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "errors (1):") {
		t.Fatalf("missing error section:\n%s", text)
	}
	if !strings.Contains(text, "index out of range") {
		t.Fatalf("missing error message:\n%s", text)
	}
	if !strings.Contains(text, "main.crash") {
		t.Fatalf("missing failing function:\n%s", text)
	}
}

// trace_summary and get_last_trace are the same call, so "last" and the explicit
// newest run id must render identically (the old get_last_trace read a different
// file and could describe a different run).
func TestSummaryResultAliasMatchesExplicitRun(t *testing.T) {
	root := recordRun(t)
	id, err := tracer.NewRunStore(root).LatestID()
	if err != nil {
		t.Fatalf("latest: %v", err)
	}

	viaAlias, err := summaryResult(root, nil, "last", "", false)
	if err != nil {
		t.Fatalf("alias: %v", err)
	}
	viaExplicit, err := summaryResult(root, nil, id, "", false)
	if err != nil {
		t.Fatalf("explicit: %v", err)
	}
	if a, b := resultText(t, viaAlias), resultText(t, viaExplicit); a != b {
		t.Fatalf("alias and explicit run differ:\n--- last ---\n%s\n--- id ---\n%s", a, b)
	}
}

// Every trace tool returns structuredContent alongside the text, so a client never
// has to parse whitespace-padded columns.
func TestSummaryResultHasStructuredContent(t *testing.T) {
	root := recordRun(t)
	res, err := summaryResult(root, nil, "last", "", false)
	if err != nil {
		t.Fatalf("summaryResult: %v", err)
	}
	out, ok := res.StructuredContent.(summaryOut)
	if !ok {
		t.Fatalf("StructuredContent is %T, want summaryOut", res.StructuredContent)
	}
	if out.RunID == "" {
		t.Fatal("run_id missing from the envelope")
	}
	if out.ExitCode != 2 {
		t.Fatalf("exit_code = %d, want 2", out.ExitCode)
	}
	if len(out.Errors) != 1 || out.Errors[0].Kind != tracer.ErrPanic {
		t.Fatalf("errors = %+v", out.Errors)
	}
	if out.Events != 1 || out.Enter != 1 {
		t.Fatalf("counts = events %d enter %d, want 1/1", out.Events, out.Enter)
	}
	if out.FullRun == "" {
		t.Fatal("full_run_file missing from the envelope")
	}
}

// The tree envelope names the slice it covers so a scoped answer is distinguishable
// from a whole-run dump.
func TestScopeOf(t *testing.T) {
	cases := []struct {
		in   treeScope
		kind string
	}{
		{treeScope{}, "all"},
		{treeScope{Ctx: "17"}, "ctx"},
		{treeScope{Fn: "ListBooks"}, "fn"},
		{treeScope{Route: "GET /books"}, "route"},
	}
	for _, tc := range cases {
		if got := scopeOf(tc.in).Kind; got != tc.kind {
			t.Fatalf("scopeOf(%+v) = %q, want %q", tc.in, got, tc.kind)
		}
	}
}
