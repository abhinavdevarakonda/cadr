package tracer

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultRetention is the number of newest runs kept on disk.
const DefaultRetention = 20

// RunMeta is the run manifest written alongside every run's event stream.
type RunMeta struct {
	Schema     int    `json:"schema"`
	ID         string `json:"id"`
	Cmd        string `json:"cmd"`
	Lang       string `json:"lang"`
	Mode       string `json:"mode"`
	Started    string `json:"started"`
	Ended      string `json:"ended,omitempty"`
	DurationMs int64  `json:"duration_ms"`
	ExitCode   int    `json:"exit_code"`
	GitRev     string `json:"git_rev,omitempty"`
	Dirty      bool   `json:"dirty"`
	EventCount int    `json:"event_count"`
	EnterCount int    `json:"enter_count"`
	ExitCount  int    `json:"exit_count"`
	Dropped    int    `json:"dropped"`
	Complete   bool   `json:"complete"`
}

// Run is a loaded run: manifest + events + summary.
type TraceRun struct {
	ID      string
	Meta    RunMeta
	Events  []Event
	Summary *Summary
}

// RunStore owns the on-disk run history under <root>/.cadr/traces.
type RunStore struct {
	Root    string
	RunsDir string
	LastPtr string
	Retain  int
}

// NewRunStore returns a store rooted at root.
func NewRunStore(root string) *RunStore {
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	traces := filepath.Join(abs, ".cadr", "traces")
	return &RunStore{
		Root:    abs,
		RunsDir: filepath.Join(traces, "runs"),
		LastPtr: filepath.Join(traces, "last_run.jsonl"),
		Retain:  DefaultRetention,
	}
}

// Ensure creates the run directories.
func (s *RunStore) Ensure() error {
	if err := os.MkdirAll(s.RunsDir, 0755); err != nil {
		return err
	}
	return os.MkdirAll(filepath.Dir(s.LastPtr), 0755)
}

func (s *RunStore) eventsPath(id string) string  { return filepath.Join(s.RunsDir, id+".jsonl") }
func (s *RunStore) metaPath(id string) string    { return filepath.Join(s.RunsDir, id+".meta.json") }
func (s *RunStore) summaryPath(id string) string { return filepath.Join(s.RunsDir, id+".summary.json") }

// NewRunID returns a sortable UTC timestamp id with a random suffix.
func NewRunID(t time.Time) string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return t.UTC().Format("20060102T150405.000Z") + "-" + hex.EncodeToString(b[:])
}

// RunWriter streams events for one run to both the per-run file and the
// backward-compatible last_run.jsonl pointer.
type RunWriter struct {
	store *RunStore
	meta  RunMeta

	mu   sync.Mutex
	f    *os.File
	bw   *bufio.Writer
	lf   *os.File
	lbw  *bufio.Writer
	evs  []Event
	done bool
}

// Create starts a new run and returns its writer.
func (s *RunStore) Create(cmd, lang, mode string) (*RunWriter, error) {
	if err := s.Ensure(); err != nil {
		return nil, err
	}
	started := time.Now().UTC()
	id := NewRunID(started)
	f, err := os.Create(s.eventsPath(id))
	if err != nil {
		return nil, err
	}
	lf, err := os.Create(s.LastPtr)
	if err != nil {
		f.Close()
		return nil, err
	}
	rev, dirty := gitState(s.Root)
	return &RunWriter{
		store: s,
		meta: RunMeta{
			Schema:  SchemaVersion,
			ID:      id,
			Cmd:     cmd,
			Lang:    lang,
			Mode:    mode,
			Started: started.Format(time.RFC3339Nano),
			GitRev:  rev,
			Dirty:   dirty,
		},
		f:   f,
		bw:  bufio.NewWriterSize(f, 64*1024),
		lf:  lf,
		lbw: bufio.NewWriterSize(lf, 64*1024),
	}, nil
}

// ID returns the run id.
func (w *RunWriter) ID() string { return w.meta.ID }

// WriteLine writes one raw JSONL line to both the run file and last_run.jsonl.
// The line is parsed so the summary can be built on Close.
func (w *RunWriter) WriteLine(line string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return nil
	}
	if _, err := w.bw.WriteString(line + "\n"); err != nil {
		return err
	}
	if _, err := w.lbw.WriteString(line + "\n"); err != nil {
		return err
	}
	if e, err := ParseEvent([]byte(line)); err == nil {
		w.evs = append(w.evs, e)
	}
	return nil
}

// WriteEvent marshals and writes one event.
func (w *RunWriter) WriteEvent(e Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return w.WriteLine(string(data))
}

// Close flushes, writes the manifest + summary and prunes old runs.
func (w *RunWriter) Close(exitCode int) error {
	w.mu.Lock()
	if w.done {
		w.mu.Unlock()
		return nil
	}
	w.done = true
	events := w.evs
	w.mu.Unlock()

	_ = w.bw.Flush()
	_ = w.lbw.Flush()
	_ = w.f.Close()
	_ = w.lf.Close()

	ended := time.Now().UTC()
	started, _ := time.Parse(time.RFC3339Nano, w.meta.Started)
	sum := Summarize(events)
	meta := w.meta
	meta.Ended = ended.Format(time.RFC3339Nano)
	meta.DurationMs = ended.Sub(started).Milliseconds()
	meta.ExitCode = exitCode
	meta.EventCount = len(events)
	meta.EnterCount = sum.EnterCount
	meta.ExitCount = sum.ExitCount
	for _, a := range sum.Anomalies {
		if a.Kind == "dropped_events" {
			meta.Dropped += 1
		}
	}
	// A full-mode run is complete only when every enter has a matching exit (an
	// interrupted server leaves open spans; light/legacy streams have no exits by
	// design).
	if meta.Mode == "light" {
		meta.Complete = meta.Dropped == 0
	} else {
		meta.Complete = meta.Dropped == 0 && sum.EnterCount == sum.ExitCount && len(BuildForest(events).OpenSpans()) == 0
	}

	if err := writeJSON(w.store.metaPath(meta.ID), meta); err != nil {
		return err
	}
	if err := writeJSON(w.store.summaryPath(meta.ID), sum); err != nil {
		return err
	}
	return w.store.Prune()
}

func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// List returns run manifests, newest first.
func (s *RunStore) List() ([]RunMeta, error) {
	entries, err := os.ReadDir(s.RunsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var metas []RunMeta
	seen := make(map[string]bool)
	// Prefer manifests.
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.RunsDir, name))
		if err != nil {
			continue
		}
		var m RunMeta
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.ID == "" {
			m.ID = strings.TrimSuffix(name, ".meta.json")
		}
		metas = append(metas, m)
		seen[m.ID] = true
	}
	// Include jsonl-only runs: interrupted recordings (killed before Close) and
	// runs written by older builds have events but no manifest.
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(name, ".jsonl")
		if seen[id] {
			continue
		}
		metas = append(metas, s.orphanMeta(id))
		seen[id] = true
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].ID > metas[j].ID })
	return metas, nil
}

// orphanMeta synthesizes a manifest for a run that has events but no
// .meta.json, so an interrupted recording is still listable/loadable. Counts are
// filled from the summary sidecar when it exists; otherwise Load computes them.
func (s *RunStore) orphanMeta(id string) RunMeta {
	m := RunMeta{Schema: SchemaVersion, ID: id}
	// The id starts with a UTC timestamp; recover it for the list view.
	if i := strings.LastIndex(id, "-"); i > 0 {
		if t, err := time.Parse("20060102T150405.000Z", id[:i]); err == nil {
			m.Started = t.UTC().Format(time.RFC3339Nano)
		}
	}
	if data, err := os.ReadFile(s.summaryPath(id)); err == nil {
		var sum Summary
		if json.Unmarshal(data, &sum) == nil {
			m.EventCount = sum.EventCount
			m.EnterCount = sum.EnterCount
			m.ExitCount = sum.ExitCount
			m.DurationMs = sum.DurationNs / int64(time.Millisecond)
			m.Complete = len(sum.Anomalies) == 0 && sum.EnterCount == sum.ExitCount && sum.EnterCount > 0
		}
		return m
	}
	// No sidecar: count lines for a useful event count without full parsing.
	if data, err := os.ReadFile(s.eventsPath(id)); err == nil {
		m.EventCount = bytes.Count(data, []byte("\n"))
	}
	return m
}

// LatestID returns the newest run id, or an error when there is no history.
func (s *RunStore) LatestID() (string, error) {
	metas, err := s.List()
	if err != nil {
		return "", err
	}
	if len(metas) == 0 {
		return "", fmt.Errorf("no runs found in %s", s.RunsDir)
	}
	return metas[0].ID, nil
}

// ResolveID maps "last"/"" to the newest run id and verifies the run exists.
func (s *RunStore) ResolveID(id string) (string, error) {
	if id == "" || id == "last" {
		return s.LatestID()
	}
	if _, err := os.Stat(s.metaPath(id)); err != nil {
		// tolerate a bare id whose meta is missing but events exist
		if _, eerr := os.Stat(s.eventsPath(id)); eerr != nil {
			return "", fmt.Errorf("run %q not found", id)
		}
	}
	return id, nil
}

// Load reads a run by id (or "last").
func (s *RunStore) Load(id string) (*TraceRun, error) {
	resolved, err := s.ResolveID(id)
	if err != nil {
		return nil, err
	}
	run := &TraceRun{ID: resolved}
	if data, err := os.ReadFile(s.metaPath(resolved)); err == nil {
		_ = json.Unmarshal(data, &run.Meta)
	}
	events, err := ReadEvents(s.eventsPath(resolved))
	if err != nil {
		return nil, err
	}
	run.Events = events
	if data, err := os.ReadFile(s.summaryPath(resolved)); err == nil {
		_ = json.Unmarshal(data, &run.Summary)
	}
	if run.Summary == nil {
		run.Summary = Summarize(events)
	}
	// Fill in a synthesized manifest for jsonl-only (interrupted) runs so
	// callers see real counts and an honest complete=false.
	if run.Meta.ID == "" {
		run.Meta.ID = resolved
	}
	if run.Meta.Schema == 0 {
		run.Meta.Schema = SchemaVersion
	}
	if run.Meta.EventCount == 0 && len(events) > 0 {
		run.Meta.EventCount = len(events)
		run.Meta.EnterCount = run.Summary.EnterCount
		run.Meta.ExitCount = run.Summary.ExitCount
		run.Meta.DurationMs = run.Summary.DurationNs / int64(time.Millisecond)
	}
	return run, nil
}

// Clear deletes a single run's files and keeps last_run.jsonl consistent.
func (s *RunStore) Clear(id string) error {
	resolved, err := s.ResolveID(id)
	if err != nil {
		return err
	}
	_ = os.Remove(s.eventsPath(resolved))
	_ = os.Remove(s.metaPath(resolved))
	_ = os.Remove(s.summaryPath(resolved))
	s.refreshLastPointer()
	return nil
}

// ClearAll deletes every run and the last_run.jsonl pointer.
func (s *RunStore) ClearAll() (int, error) {
	metas, err := s.List()
	if err != nil {
		return 0, err
	}
	s.removeFiles(metas)
	_ = os.Remove(s.LastPtr)
	return len(metas), nil
}

// Keep deletes all but the newest n runs and returns the number removed.
func (s *RunStore) Keep(n int) (int, error) {
	metas, err := s.List()
	if err != nil {
		return 0, err
	}
	if n < 0 {
		n = 0
	}
	if n >= len(metas) {
		return 0, nil
	}
	removed := s.removeFiles(metas[n:])
	s.refreshLastPointer()
	return removed, nil
}

// removeFiles deletes the run files for metas and returns the count.
func (s *RunStore) removeFiles(metas []RunMeta) int {
	for _, m := range metas {
		_ = os.Remove(s.eventsPath(m.ID))
		_ = os.Remove(s.metaPath(m.ID))
		_ = os.Remove(s.summaryPath(m.ID))
	}
	return len(metas)
}

// refreshLastPointer makes last_run.jsonl a copy of the newest remaining run,
// or removes it when there are no runs left.
func (s *RunStore) refreshLastPointer() {
	metas, err := s.List()
	if err != nil || len(metas) == 0 {
		_ = os.Remove(s.LastPtr)
		return
	}
	data, err := os.ReadFile(s.eventsPath(metas[0].ID))
	if err != nil {
		_ = os.Remove(s.LastPtr)
		return
	}
	_ = os.WriteFile(s.LastPtr, data, 0644)
}

// Prune deletes all but the newest Retain runs.
func (s *RunStore) Prune() error {
	metas, err := s.List()
	if err != nil {
		return err
	}
	retain := s.Retain
	if retain <= 0 {
		retain = DefaultRetention
	}
	for i := retain; i < len(metas); i++ {
		id := metas[i].ID
		_ = os.Remove(s.eventsPath(id))
		_ = os.Remove(s.metaPath(id))
		_ = os.Remove(s.summaryPath(id))
	}
	return nil
}

func gitState(root string) (string, bool) {
	rev := ""
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = root
	if out, err := cmd.Output(); err == nil {
		rev = strings.TrimSpace(string(out))
	}
	dirty := false
	cmd = exec.Command("git", "status", "--porcelain")
	cmd.Dir = root
	if out, err := cmd.Output(); err == nil && len(strings.TrimSpace(string(out))) > 0 {
		dirty = true
	}
	return rev, dirty
}
