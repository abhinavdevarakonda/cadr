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
