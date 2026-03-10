package tracer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Event struct {
	Event     string  `json:"event"`
	Name      string  `json:"name"`
	File      string  `json:"file"`
	Line      int     `json:"line"`
	Timestamp float64 `json:"timestamp"`
}

type Recording struct {
	Events []Event
}

// Run starts a command with the appropriate tracer and streams hits to a callback.
func Run(fullCmd string, onEvent func(Event)) error {
	parts := strings.Fields(fullCmd)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	var cmd *exec.Cmd
	// If it's a python command, we wrap it with our tracer script
	if parts[0] == "python" || parts[0] == "python3" {
		scriptPath := "/home/abee/code/projects/maplet/internal/tracer/py_trace.py"
		newArgs := append([]string{scriptPath}, parts[1:]...)
		cmd = exec.Command(parts[0], newArgs...)
	} else {
		// Run exactly what was requested (e.g. "go run app.go")
		cmd = exec.Command(parts[0], parts[1:]...)
	}

	cmd.Stdin = os.Stdin
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	// 1. App Output (Stage) - Echo in background
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			fmt.Println(scanner.Text())
		}
	}()

	// 2. Maplet "Hears" the app (Backstage) via Stderr
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		onEvent(event)
	}

	return cmd.Wait()
}
