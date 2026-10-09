package agents

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type pyEvent struct {
	Fn   string `json:"fn"`
	File string `json:"file"`
	Line int    `json:"line"`
	Ev   string `json:"ev"`
	Ctx  string `json:"ctx"`
	Span int64  `json:"span"`
	TS   int64  `json:"ts"`
}

func TestPythonEmitterCallTree(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns python")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}

	agent, ok := Get("python")
	if !ok {
		t.Fatal("python agent not registered")
	}
	// Hook files live outside the project root so py_trace's own frames are
	// filtered out, exactly as in real usage.
	projectDir := t.TempDir()
	hookDir := t.TempDir()
	for _, name := range []string{"py_trace.py", "sitecustomize.py"} {
		data, err := agent.Files.ReadFile(name)
		if err != nil {
			t.Fatalf("read embedded %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(hookDir, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	app := "import time\n\n\ndef leaf():\n    time.sleep(0.03)\n    return 1\n\n\ndef mid():\n    return leaf() + leaf()\n\n\ndef top():\n    return mid()\n\n\nif __name__ == \"__main__\":\n    print(top())\n"
	if err := os.WriteFile(filepath.Join(projectDir, "app.py"), []byte(app), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(python, "app.py")
	cmd.Dir = projectDir
	cmd.Env = append(os.Environ(),
		"PYTHONPATH="+hookDir,
		"CADR_TRACE=1",
		"CADR_LOCAL_ONLY=1",
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("python run failed: %v\n%s", err, stderr.String())
	}

	var events []pyEvent
	for _, line := range strings.Split(stderr.String(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var e pyEvent
		if json.Unmarshal([]byte(line), &e) == nil && e.Span > 0 {
			events = append(events, e)
		}
	}
	// top -> mid -> leaf, leaf = 4 enters + 4 exits
	if len(events) != 8 {
		t.Fatalf("got %d events, want 8:\n%s", len(events), stderr.String())
	}
	// Every enter must have a matching exit; top outlives mid which outlives leaf.
	span := map[int64]pyEvent{}
	exit := map[int64]int64{}
	for _, e := range events {
		if e.Ev == "enter" {
			span[e.Span] = e
		} else {
			exit[e.Span] = e.TS
		}
	}
	if len(span) != 4 || len(exit) != 4 {
		t.Fatalf("unbalanced: enters=%d exits=%d", len(span), len(exit))
	}
	for s, e := range span {
		if exit[s] < e.TS {
			t.Fatalf("exit before enter for span %d", s)
		}
	}
	leafDur := int64(0)
	for s, e := range span {
		if strings.HasSuffix(e.File, "app.py") && e.Fn == "leaf" {
			leafDur = exit[s] - e.TS
		}
	}
	if leafDur < int64(20*1e6) {
		t.Fatalf("leaf duration = %dns, want >= 20ms", leafDur)
	}
}
