package toolwrap

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestMain doubles as the -toolexec entry point so the integration test can use
// the test binary as its wrapper.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "tool-wrap" {
		os.Exit(Run(os.Args[2:]))
	}
	os.Exit(m.Run())
}

type traceEvent struct {
	Fn   string `json:"fn"`
	File string `json:"file"`
	Line int    `json:"line"`
	Ev   string `json:"ev"`
	Span int64  `json:"span"`
	TS   int64  `json:"ts"`
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func hasEvent(events []traceEvent, fn, file string, line int) bool {
	for _, e := range events {
		if e.Fn == fn && e.File == file && e.Line == line {
			return true
		}
	}
	return false
}

func TestInstrumentedGoRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket transport test")
	}
	if testing.Short() {
		t.Skip("builds a Go module")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}

	mod := t.TempDir()
	mainSrc := "package main\n\nimport \"time\"\n\nfunc Foo() int {\n\treturn 1\n}\n\nfunc main() {\n\t_ = Foo()\n\ttime.Sleep(500 * time.Millisecond)\n}\n"
	writeFile(t, filepath.Join(mod, "go.mod"), "module cadrtest\n\ngo 1.26\n")
	writeFile(t, filepath.Join(mod, "main.go"), mainSrc)

	// A stable cache base keeps the stdlib closure warm across `go test` runs.
	cacheBase := filepath.Join(os.TempDir(), "cadr-toolwrap-test-cache")
	env, ok := prepareEnv(mod, cacheBase)
	if !ok {
		t.Fatal("prepareEnv failed: go tracing disabled")
	}

	// Cache isolation: a preceding normal build (warm default GOCACHE) must not
	// suppress instrumentation.
	normal := exec.Command("go", "build", "-o", filepath.Join(mod, "app"), ".")
	normal.Dir = mod
	if out, err := normal.CombinedOutput(); err != nil {
		t.Fatalf("normal build failed: %v\n%s", err, out)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	spec := ToolexecValue(exe)

	mainFile := filepath.Join(mod, "main.go")
	if resolved, err := filepath.EvalSymlinks(mod); err == nil {
		mainFile = filepath.Join(resolved, "main.go")
	}
	for run := 0; run < 2; run++ {
		events := runTraced(t, mod, env, spec)
		if !hasEvent(events, "Foo", mainFile, 5) {
			t.Fatalf("run %d: missing Foo@%s:5; got %+v", run, mainFile, events)
		}
		if !hasEvent(events, "main", mainFile, 9) {
			t.Fatalf("run %d: missing main@%s:9; got %+v", run, mainFile, events)
		}

		// Every enter must have a matching exit carrying the same span.
		for _, e := range events {
			if e.Ev != "enter" || e.Span == 0 {
				continue
			}
			if d, ok := exitDuration(events, e.Span); !ok {
				t.Fatalf("run %d: enter %s span %d has no exit; got %+v", run, e.Fn, e.Span, events)
			} else if e.Fn == "main" && d < int64(400*time.Millisecond) {
				t.Fatalf("run %d: main duration = %s, want >= 400ms", run, time.Duration(d))
			}
		}
	}
}

// exitDuration returns the duration (ns) recorded by the exit matching span.
func exitDuration(events []traceEvent, span int64) (int64, bool) {
	var start int64 = -1
	for _, e := range events {
		if e.Span != span {
			continue
		}
		if e.Ev == "enter" {
			start = e.TS
		} else if e.Ev == "exit" && start >= 0 {
			return e.TS - start, true
		}
	}
	return 0, false
}

// TestOverlayGoRun exercises the shared-runtime overlay build: one process-wide
// runtime, so a cross-package call nests correctly and spans are unique.
func TestOverlayGoRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket transport test")
	}
	if testing.Short() {
		t.Skip("builds a Go module")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}

	mod := t.TempDir()
	writeFile(t, filepath.Join(mod, "go.mod"), "module overlaytest\n\ngo 1.26\n")
	writeFile(t, filepath.Join(mod, "main.go"), "package main\n\nimport \"overlaytest/lib\"\n\nfunc main() { lib.A() }\n")
	if err := os.MkdirAll(filepath.Join(mod, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(mod, "lib", "lib.go"), "package lib\n\nfunc A() { B() }\n\nfunc B() {}\n")

	ob, err := BuildOverlay(mod, filepath.Join(mod, ".cadr", "cache"))
	if err != nil {
		t.Fatalf("BuildOverlay: %v", err)
	}
	if ob.Count == 0 {
		t.Fatal("no files instrumented")
	}

	events := captureEvents(t, mod, ob.Env, []string{"run", "-overlay=" + ob.OverlayPath, "-modfile=" + ob.ModFile, "."})

	// Unique spans across packages.
	spans := map[int64]int{}
	var enterOrder []string
	for _, e := range events {
		if e.Ev != "enter" {
			continue
		}
		spans[e.Span]++
		enterOrder = append(enterOrder, e.Fn)
	}
	for s, n := range spans {
		if n > 1 {
			t.Fatalf("span %d used %d times", s, n)
		}
	}
	if len(enterOrder) < 3 {
		t.Fatalf("enter order = %v", enterOrder)
	}
	// main must arrive before lib.A before lib.B (single sender, exact order).
	if enterOrder[0] != "main" || enterOrder[1] != "A" || enterOrder[2] != "B" {
		t.Fatalf("cross-package order = %v (events=%d)", enterOrder, len(events))
	}
}

// captureEvents runs a go subcommand and returns the trace events streamed over a
// fresh unix socket.
func captureEvents(t *testing.T, dir string, env []string, goArgs []string) []traceEvent {
	t.Helper()
	sock := filepath.Join("/tmp", fmt.Sprintf("cadr-ovl-%d.sock", time.Now().UnixNano()))
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	events := make(chan traceEvent, 1024)
	var wg sync.WaitGroup
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					var e traceEvent
					if json.Unmarshal(sc.Bytes(), &e) == nil {
						select {
						case events <- e:
						default:
						}
					}
				}
			}(conn)
		}
	}()

	cmd := exec.Command("go", goArgs...)
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, env...), "CADR_SOCKET="+sock)
	out, err := cmd.CombinedOutput()
	_ = ln.Close()
	if err != nil {
		t.Fatalf("go %v failed: %v\n%s", goArgs, err, out)
	}
	wg.Wait()

	var got []traceEvent
	timeout := time.After(2 * time.Second)
	for {
		select {
		case e := <-events:
			got = append(got, e)
		case <-timeout:
			t.Logf("captureEvents go %v out=%q got=%d", goArgs, string(out), len(got))
			return got
		}
	}
}

// runTraced runs `go run .` under the wrapper and returns the events the
// runtime streams over the unix socket.
func runTraced(t *testing.T, dir string, env []string, spec string) []traceEvent {
	t.Helper()

	sock := filepath.Join("/tmp", fmt.Sprintf("cadr-test-%d.sock", time.Now().UnixNano()))
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	events := make(chan traceEvent, 1024)
	var wg sync.WaitGroup
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					var e traceEvent
					if json.Unmarshal(sc.Bytes(), &e) == nil {
						select {
						case events <- e:
						default:
						}
					}
				}
			}(conn)
		}
	}()

	cmd := exec.Command("go", "run", "-toolexec="+spec, ".")
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, env...), "CADR_SOCKET="+sock)
	out, err := cmd.CombinedOutput()
	_ = ln.Close()
	if err != nil {
		t.Fatalf("go run failed: %v\n%s", err, out)
	}
	wg.Wait()

	var got []traceEvent
	timeout := time.After(2 * time.Second)
	for {
		select {
		case e := <-events:
			got = append(got, e)
		case <-timeout:
			return got
		}
	}
}
