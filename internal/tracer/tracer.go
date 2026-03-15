package tracer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
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

func Run(fullCmd string, onEvent func(Event)) error {
	return runCmd(fullCmd, false, onEvent)
}

func RunLocal(fullCmd string, onEvent func(Event)) error {
	return runCmd(fullCmd, true, onEvent)
}

func runCmd(fullCmd string, localOnly bool, onEvent func(Event)) error {
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

	cmd.Env = os.Environ()
	if localOnly {
		cmd.Env = append(cmd.Env, "MAPLET_LOCAL_ONLY=1")
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

	// 2. Maplet "Hears" the app via Stderr
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			// Not a trace event? Echo it to the user's stderr so they see logs/errors
			fmt.Fprintln(os.Stderr, line)
			continue
		}
		onEvent(event)
	}

	return cmd.Wait()
}

// Listen starts a TCP server on port 9876 to receive hits from external processes (e.g. Docker, separate terminal).
func Listen(onEvent func(Event)) error {
	ln, err := net.Listen("tcp", "localhost:9876")
	if err != nil {
		return err
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}

		go func(c net.Conn) {
			defer c.Close()
			scanner := bufio.NewScanner(c)
			for scanner.Scan() {
				var event Event
				if err := json.Unmarshal([]byte(scanner.Text()), &event); err == nil {
					onEvent(event)
				}
			}
		}(conn)
	}
}
