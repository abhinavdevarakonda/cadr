package tracer

import (
	"bufio"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

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

type Config struct {
	Protocol string // "uds" or "tcp"
	Port     string // "9876"
	Socket   string // path to unix socket
}

func ResolveSocketPath(root string) string {
	if envSock := os.Getenv("CADR_SOCKET"); envSock != "" {
		return envSock
	}
	if root == "" || root == "." {
		root, _ = os.Getwd()
	}
	abs, err := filepath.Abs(root)
	if err == nil {
		root = abs
	}
	root = filepath.Clean(root)
	h := md5.Sum([]byte(root))
	dir := "/tmp"
	if runtime.GOOS == "windows" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, fmt.Sprintf("cadr-%x.sock", h[:6]))
}

func LoadConfig(root string) Config {
	cfg := Config{
		Protocol: "uds",
		Port:     "9876",
	}
	if runtime.GOOS == "windows" {
		cfg.Protocol = "tcp"
	}

	cfgPath := filepath.Join(root, ".cadr", "config.yaml")
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		cfgPath = filepath.Join(root, "cadr.yaml")
	}

	if data, err := os.ReadFile(cfgPath); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, l := range lines {
			trimmed := strings.TrimSpace(l)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) != 2 {
				continue
			}
			k := strings.ToLower(strings.TrimSpace(parts[0]))
			v := strings.TrimSpace(parts[1])
			if k == "protocol" {
				cfg.Protocol = strings.ToLower(v)
			} else if k == "port" || k == "tcp_port" {
				cfg.Port = v
			} else if k == "socket" || k == "uds_path" {
				cfg.Socket = v
			}
		}
	}

	if os.Getenv("CADR_FORCE_TCP") == "1" {
		cfg.Protocol = "tcp"
		if p := os.Getenv("CADR_TCP_PORT"); p != "" {
			cfg.Port = p
		}
	}

	if cfg.Socket == "" {
		cfg.Socket = ResolveSocketPath(root)
	}

	return cfg
}

func Run(fullCmd string, onEvent func(Event)) error {
	return runCmd(fullCmd, "", false, onEvent)
}

func RunLocal(fullCmd string, onEvent func(Event)) error {
	return runCmd(fullCmd, "", true, onEvent)
}

func RunWithLang(fullCmd string, langOverride string, onEvent func(Event)) error {
	return runCmd(fullCmd, langOverride, false, onEvent)
}

func RunLocalWithLang(fullCmd string, langOverride string, onEvent func(Event)) error {
	return runCmd(fullCmd, langOverride, true, onEvent)
}

func runCmd(fullCmd string, langOverride string, localOnly bool, onEvent func(Event)) error {
	lang := langOverride
	if lang == "" {
		lang = agents.DetectLanguage(fullCmd)
	}
	if lang == "" {
		return fmt.Errorf("unsupported language for command: %s", fullCmd)
	}

	agent, ok := agents.Get(lang)
	if !ok {
		return fmt.Errorf("no agent registered for language: %s", lang)
	}

	parts := agents.SplitCommand(fullCmd)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	cmd := exec.Command(parts[0], parts[1:]...)

	cmd.Env = os.Environ()

	if localOnly {
		cmd.Env = append(cmd.Env, "CADR_LOCAL_ONLY=1")
	}

	// Inject UDS / TCP settings into the child process environment
	cfg := LoadConfig(".")
	if cfg.Protocol == "tcp" {
		cmd.Env = append(cmd.Env, "CADR_TCP="+cfg.Port)
	} else {
		cmd.Env = append(cmd.Env, "CADR_SOCKET="+cfg.Socket)
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
	cmd.Stdout = os.Stdout
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

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

type Listener struct {
	net.Listener
	socketPath string
	closeOnce  sync.Once
}

func (l *Listener) Close() error {
	var err error
	l.closeOnce.Do(func() {
		err = l.Listener.Close()
		if l.socketPath != "" {
			_ = os.Remove(l.socketPath)
		}
	})
	return err
}

func StartListener(root string, onEvent func(Event)) (*Listener, error) {
	cfg := LoadConfig(root)
	return StartListenerWithConfig(cfg, onEvent)
}

func StartListenerWithConfig(cfg Config, onEvent func(Event)) (*Listener, error) {
	var ln net.Listener
	var err error

	if cfg.Protocol == "tcp" {
		port := cfg.Port
		if port == "" {
			port = "9876"
		}
		ln, err = net.Listen("tcp", "localhost:"+port)
	} else {
		_ = os.Remove(cfg.Socket)
		ln, err = net.Listen("unix", cfg.Socket)
	}

	if err != nil {
		return nil, err
	}

	l := &Listener{Listener: ln}
	if cfg.Protocol != "tcp" {
		l.socketPath = cfg.Socket
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
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
	}()

	return l, nil
}

func Listen(onEvent func(Event)) error {
	return ListenWithRoot(".", onEvent)
}

func ListenWithRoot(root string, onEvent func(Event)) error {
	cfg := LoadConfig(root)
	return ListenWithConfig(cfg, onEvent)
}

func ListenWithConfig(cfg Config, onEvent func(Event)) error {
	l, err := StartListenerWithConfig(cfg, onEvent)
	if err != nil {
		return err
	}
	defer l.Close()
	select {}
}
