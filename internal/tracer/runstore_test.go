package tracer

import (
	"os"
	"path/filepath"
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
	if _, err := os.Stat(store.LastPtr); err != nil {
		t.Fatalf("missing last_run.jsonl: %v", err)
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
	if _, err := os.Stat(store.LastPtr); !os.IsNotExist(err) {
		t.Fatalf("last_run.jsonl should be gone after ClearAll")
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
