package tracer

import (
	"bufio"
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// embed sitecustomize.py py_trace.py
var hookFiles embed.FS

// ensureHookDir unpacks the embedded Python files into a local cache directory
// so that PYTHONPATH can point to them on any machine.
func ensureHookDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	hookDir := filepath.Join(home, ".maplet", "hooks")
	if err := os.MkdirAll(hookDir, 0755); err != nil {
		return "", err
	}

	for _, name := range []string{"sitecustomize.py", "py_trace.py"} {
		content, err := hookFiles.ReadFile(name)
		if err != nil {
			return "", err
		}
		destPath := filepath.Join(hookDir, name)

		if err := os.WriteFile(destPath, content, 0644); err != nil {
			return "", err
		}
	}
	return hookDir, nil
}

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

	cmd := exec.Command(parts[0], parts[1:]...)

	cmd.Env = os.Environ()
	if localOnly {
		cmd.Env = append(cmd.Env, "MAPLET_LOCAL_ONLY=1")
	}

	// Inject Maplet universal Python hook via PYTHONPATH
	cmd.Env = append(cmd.Env, "MAPLET_TRACE=1")

	hookDir, err := ensureHookDir()
	if err != nil {
		return fmt.Errorf("failed to setup maplet hooks: %v", err)
	}

	pythonPathFound := false
	for i, env := range cmd.Env {
		if strings.HasPrefix(env, "PYTHONPATH=") {
			newPath := hookDir + string(os.PathListSeparator) + strings.TrimPrefix(env, "PYTHONPATH=")
			cmd.Env[i] = "PYTHONPATH=" + newPath
			pythonPathFound = true
			break
		}
	}
	if !pythonPathFound {
		cmd.Env = append(cmd.Env, "PYTHONPATH="+hookDir)
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
