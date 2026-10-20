package tracer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunStoreLifecycle(t *testing.T) {
	root := t.TempDir()
	store := NewRunStore(root)

	w, err := store.Create("go run .", "go", "full")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	events := []Event{
		ev("main", EvEnter, "1", 1, 0),
		ev("child", EvEnter, "1", 2, 1),
		ev("", EvExit, "1", 2, 5),
		ev("", EvExit, "1", 1, 9),
	}
	for _, e := range events {
		if err := w.WriteEvent(e); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := w.Close(0); err != nil {
		t.Fatalf("close: %v", err)
	}

	for _, suffix := range []string{".jsonl", ".meta.json", ".summary.json"} {
		p := filepath.Join(store.RunsDir, w.ID()+suffix)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing %s: %v", p, err)
		}
	}
	// last_run.jsonl is no longer written: the run store is the single source of
	// truth for "last".
	if _, err := os.Stat(store.legacyLastPtr()); !os.IsNotExist(err) {
		t.Fatalf("last_run.jsonl should no longer be written")
	}

	metas, err := store.List()
	if err != nil || len(metas) != 1 {
		t.Fatalf("list = %+v err=%v", metas, err)
	}
	if metas[0].Schema != SchemaVersion || metas[0].Cmd != "go run ." || metas[0].ExitCode != 0 {
		t.Fatalf("meta = %+v", metas[0])
	}
	if metas[0].EventCount != 4 || metas[0].EnterCount != 2 || metas[0].ExitCount != 2 {
		t.Fatalf("counts = %+v", metas[0])
	}
	if !metas[0].Complete {
		t.Fatalf("expected complete manifest: %+v", metas[0])
	}

	run, err := store.Load("last")
	if err != nil {
		t.Fatalf("load last: %v", err)
	}
	if len(run.Events) != 4 {
		t.Fatalf("loaded events = %d", len(run.Events))
	}
	if run.Summary == nil || len(run.Summary.Functions) != 2 {
		t.Fatalf("summary = %+v", run.Summary)
	}
}

func TestRunStoreRetention(t *testing.T) {
	root := t.TempDir()
	store := NewRunStore(root)
	store.Retain = 2

	ids := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		w, err := store.Create("cmd", "go", "full")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		_ = w.WriteEvent(ev("main", EvEnter, "1", 1, 0))
		if err := w.Close(0); err != nil {
			t.Fatalf("close: %v", err)
		}
		ids = append(ids, w.ID())
	}

	metas, err := store.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("retention kept %d runs, want 2", len(metas))
	}
	// The oldest run's files must be gone.
	oldest := ids[0]
	for _, suffix := range []string{".jsonl", ".meta.json", ".summary.json"} {
		if _, err := os.Stat(filepath.Join(store.RunsDir, oldest+suffix)); !os.IsNotExist(err) {
			t.Fatalf("oldest run file %s still exists", oldest+suffix)
		}
	}
}

func TestRunStoreOrphanRun(t *testing.T) {
	root := t.TempDir()
	store := NewRunStore(root)
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}
	id := "20260101T000000.000Z-orphan"
	line := `{"lang":"go","fn":"main","file":"/p/m.go","line":1,"ev":"enter","ctx":"1","span":1,"seq":1,"pid":1,"ts":0}`
	if err := os.WriteFile(store.eventsPath(id), []byte(line+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	metas, err := store.List()
	if err != nil || len(metas) != 1 {
		t.Fatalf("list=%v err=%v", metas, err)
	}
	if metas[0].ID != id || metas[0].Schema != SchemaVersion {
		t.Fatalf("meta=%+v", metas[0])
	}

	run, err := store.Load("last")
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Events) != 1 || run.Meta.EventCount != 1 || run.Meta.EnterCount != 1 {
		t.Fatalf("run meta=%+v events=%d", run.Meta, len(run.Events))
	}
	if run.Meta.Complete {
		t.Fatalf("interrupted run must not be complete")
	}
}

func TestRunStoreClearAndKeep(t *testing.T) {
	root := t.TempDir()
	store := NewRunStore(root)
	var ids []string
	for i := 0; i < 3; i++ {
		w, err := store.Create("cmd", "go", "full")
		if err != nil {
			t.Fatal(err)
		}
		_ = w.WriteEvent(ev("main", EvEnter, "1", 1, 0))
		if err := w.Close(0); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, w.ID())
	}

	if err := store.Clear(ids[0]); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if metas, _ := store.List(); len(metas) != 2 {
		t.Fatalf("after clear: %d runs", len(metas))
	}

	removed, err := store.Keep(1)
	if err != nil || removed != 1 {
		t.Fatalf("keep removed=%d err=%v", removed, err)
	}
	if metas, _ := store.List(); len(metas) != 1 {
		t.Fatalf("after keep: %d runs", len(metas))
	}

	removed, err = store.ClearAll()
	if err != nil || removed != 1 {
		t.Fatalf("clearall removed=%d err=%v", removed, err)
	}
	if metas, _ := store.List(); len(metas) != 0 {
		t.Fatalf("after clearall: %d runs", len(metas))
	}
	if _, err := os.Stat(store.legacyLastPtr()); !os.IsNotExist(err) {
		t.Fatalf("last_run.jsonl should be gone after ClearAll")
	}
}

// A pre-v3 traces/last_run.jsonl with no manifest is adopted as an orphan run so
// old traces stay visible, and adoption is idempotent.
func TestRunStoreAdoptsLegacyPointer(t *testing.T) {
	root := t.TempDir()
	store := NewRunStore(root)
	legacy := store.legacyLastPtr()
	if err := os.MkdirAll(filepath.Dir(legacy), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"fn":"main","file":"main.go","line":1}` + "\n" +
		`{"fn":"helper","file":"main.go","line":9}` + "\n"
	if err := os.WriteFile(legacy, []byte(body), 0644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	metas, err := store.List()
	if err != nil || len(metas) != 1 {
		t.Fatalf("list = %+v err=%v", metas, err)
	}
	if !strings.HasSuffix(metas[0].ID, "-legacy") {
		t.Fatalf("adopted id = %q, want a -legacy suffix", metas[0].ID)
	}

	// Listing again must not create a second copy.
	if metas, _ := store.List(); len(metas) != 1 {
		t.Fatalf("second list = %d runs, want 1", len(metas))
	}

	run, err := store.Load("last")
	if err != nil {
		t.Fatalf("load last: %v", err)
	}
	if len(run.Events) != 2 {
		t.Fatalf("adopted events = %d, want 2", len(run.Events))
	}

	if _, err := store.ClearAll(); err != nil {
		t.Fatalf("clearall: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy pointer should be removed by ClearAll")
	}
	if metas, _ := store.List(); len(metas) != 0 {
		t.Fatalf("cleared store re-adopted %d run(s)", len(metas))
	}
}

// Failures recovered from stderr are written to a sidecar and read back by Load;
// the event stream stays untouched so the tree and counts are unaffected.
func TestRunStoreErrorsRoundTrip(t *testing.T) {
	root := t.TempDir()
	store := NewRunStore(root)

	w, err := store.Create("go run .", "go", "full")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_ = w.WriteEvent(ev("main", EvEnter, "1", 1, 1))
	_ = w.WriteEvent(ev("main", EvExit, "1", 1, 2))
	d := NewDiagnostics()
	for _, line := range []string{
		"panic: runtime error: index out of range [7] with length 3",
		"",
		"goroutine 1 [running]:",
		"main.handler(...)",
		"\t/app/handlers.go:42 +0x1a5",
	} {
		d.Add(line)
	}
	w.SetDiagnostics(d)
	if err := w.Close(2); err != nil {
		t.Fatalf("close: %v", err)
	}

	metas, err := store.List()
	if err != nil || len(metas) != 1 {
		t.Fatalf("list = %+v err=%v", metas, err)
	}
	if metas[0].ErrorCount != 1 {
		t.Fatalf("error_count = %d, want 1", metas[0].ErrorCount)
	}
	if !strings.Contains(metas[0].FirstError, "index out of range") {
		t.Fatalf("first_error = %q", metas[0].FirstError)
	}

	run, err := store.Load("last")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(run.Errors) != 1 {
		t.Fatalf("loaded %d errors, want 1", len(run.Errors))
	}
	if run.Errors[0].Fn != "main.handler" || run.Errors[0].Line != 42 {
		t.Fatalf("error = %+v", run.Errors[0])
	}
	// The raw stderr tail is kept so a failure without a recognisable panic is
	// still diagnosable.
	if len(run.Stderr) != 4 {
		t.Fatalf("stderr tail = %d lines, want 4 (blank lines dropped)", len(run.Stderr))
	}
	// The event stream is unaffected.
	if len(run.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(run.Events))
	}
}

func TestResolveIDUnknown(t *testing.T) {
	store := NewRunStore(t.TempDir())
	if _, err := store.ResolveID("nope"); err == nil {
		t.Fatal("expected error for unknown run")
	}
	if _, err := store.LatestID(); err == nil {
		t.Fatal("expected error for empty store")
	}
}
