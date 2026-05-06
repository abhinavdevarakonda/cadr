package tracer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/abhinavdevarakonda/cadr/internal/agents"
)

type Event struct {
	Name string                 `json:"fn"`
	File string                 `json:"file"`
	Line int                    `json:"line"`
	Args map[string]interface{} `json:"args"`
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
	lang := agents.DetectLanguage(fullCmd)
	if lang == "" {
		return fmt.Errorf("unsupported language for command: %s", fullCmd)
	}

	agent, ok := agents.Get(lang)
	if !ok {
		return fmt.Errorf("no agent registered for language: %s", lang)
	}

	parts := strings.Fields(fullCmd)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	cmd := exec.Command(parts[0], parts[1:]...)

	cmd.Env = os.Environ()

	if localOnly {
		cmd.Env = append(cmd.Env, "CADR_LOCAL_ONLY=1")
	}

	hookDir, err := agents.SetupHookDir(agent)
	if err != nil {
		return fmt.Errorf("failed to setup agent hooks: %v", err)
	}

	if agent.EnvVar != "" {
		if agent.EnvValue != "" {
			// direct value mode (e.g., NODE_OPTIONS="--require /path/to/js_trace.js")
			value := strings.ReplaceAll(agent.EnvValue, "{hookDir}", hookDir)
			pathFound := false
			for i, env := range cmd.Env {
				if strings.HasPrefix(env, agent.EnvVar+"=") {
					existing := strings.TrimPrefix(env, agent.EnvVar+"=")
					cmd.Env[i] = agent.EnvVar + "=" + value + " " + existing
					pathFound = true
					break
				}
			}
			if !pathFound {
				cmd.Env = append(cmd.Env, agent.EnvVar+"="+value)
			}
		} else {
			// PATH-style prepend mode (e.g., PYTHONPATH)
			pathFound := false
			for i, env := range cmd.Env {
				if strings.HasPrefix(env, agent.EnvVar+"=") {
					newPath := hookDir + string(os.PathListSeparator) + strings.TrimPrefix(env, agent.EnvVar+"=")
					cmd.Env[i] = agent.EnvVar + "=" + newPath
					pathFound = true
					break
				}
			}
			if !pathFound {
				cmd.Env = append(cmd.Env, agent.EnvVar+"="+hookDir)
			}
		}
	}

	if agent.TraceEnvVar != "" {
		cmd.Env = append(cmd.Env, agent.TraceEnvVar+"="+agent.TraceEnvValue)
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

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			fmt.Println(scanner.Text())
		}
	}()

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
