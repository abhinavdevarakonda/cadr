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

// Event is the language-neutral trace event (schema v2). Legacy v1 lines only
// carry fn/file/line/args; the missing fields are zero-valued and the event is
// treated as an enter (see NormalizeEvent).
type Event struct {
	Lang string                 `json:"lang,omitempty"`
	Name string                 `json:"fn"`
	File string                 `json:"file"`
	Line int                    `json:"line"`
	Ev   string                 `json:"ev,omitempty"` // enter | exit ("" = legacy enter)
	Ctx  string                 `json:"ctx,omitempty"`
	Span int64                  `json:"span,omitempty"`
	Seq  int64                  `json:"seq,omitempty"`
	PID  int                    `json:"pid,omitempty"`
	TS   int64                  `json:"ts,omitempty"`
	Args map[string]interface{} `json:"args,omitempty"`
	Sym  string                 `json:"sym,omitempty"`
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
	// Resolve symlinks so `cadr /tmp/app` and `cadr run` executed in
	// /private/tmp/app (macOS) compute the same socket path.
	if resolved, rerr := filepath.EvalSymlinks(root); rerr == nil {
		root = resolved
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
	return RunCommand(fullCmd, RunOptions{OnEvent: onEvent})
}

func RunLocal(fullCmd string, onEvent func(Event)) error {
	return RunCommand(fullCmd, RunOptions{OnEvent: onEvent, LocalOnly: true})
}

func RunWithLang(fullCmd string, langOverride string, onEvent func(Event)) error {
	return RunCommand(fullCmd, RunOptions{OnEvent: onEvent, LangOverride: langOverride})
}

func RunLocalWithLang(fullCmd string, langOverride string, onEvent func(Event)) error {
	return RunCommand(fullCmd, RunOptions{OnEvent: onEvent, LangOverride: langOverride, LocalOnly: true})
}

// RunOptions configures a traced run.
type RunOptions struct {
	LangOverride string
	LocalOnly    bool
	OnEvent      func(Event)
	// OnDiagnostic receives every stderr line that is not a trace event: app
	// output, log lines, panic traces. Optional; when nil those lines are
	// forwarded to os.Stderr.
	OnDiagnostic func(line string)
}

// RunCommand runs fullCmd under instrumentation, reporting trace events to
// opts.OnEvent and every other stderr line to opts.OnDiagnostic.
func RunCommand(fullCmd string, opts RunOptions) error {
	lang := opts.LangOverride
	if lang == "" {
		lang = agents.DetectLanguage(fullCmd)
	}
	if lang == "" {
		return fmt.Errorf("unsupported language for command: %s", fullCmd)
	}

	if lang == "go" {
		return runGoCmd(fullCmd, opts)
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

	if opts.LocalOnly {
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
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if e, ok := ParseEventLine([]byte(line)); ok {
			if opts.OnEvent != nil {
				opts.OnEvent(e)
			}
			continue
		}
		// Not a trace event: app output, a log line or a panic.
		if opts.OnDiagnostic != nil {
			opts.OnDiagnostic(line)
			continue
		}
		fmt.Fprintln(os.Stderr, line)
	}

	return cmd.Wait()
}

// diagEnvelope marks a crash record on the event transport so a listener can tell it
// apart from a trace event. `cadr run` uses it to forward what the child printed on
// stderr to a TUI listening on the socket.
type diagEnvelope struct {
	Cadr   string       `json:"cadr"` // always "diag"
	Errors []TraceError `json:"errors"`
}

// ParseDiagLine reports whether a transport line carries forwarded crash records.
// The whole set travels in one line so a consumer can replace its view rather than
// accumulate half-filled records as a stack grows.
func ParseDiagLine(line []byte) ([]TraceError, bool) {
	var env diagEnvelope
	if err := json.Unmarshal(line, &env); err != nil || env.Cadr != "diag" {
		return nil, false
	}
	return env.Errors, true
}

// SendDiagnostics forwards one batch of crash records to whatever is listening on
// cfg. Prefer DiagSender when sending repeatedly: each batch must arrive in order,
// and separate connections are handled by separate consumer goroutines.
func SendDiagnostics(cfg Config, errs []TraceError) {
	s := NewDiagSender(cfg)
	defer s.Close()
	s.Send(errs)
}

// DiagSender forwards crash records to a listener over a single connection, so
// batches arrive in the order they were produced. A missing listener is not an
// error: `cadr run` works without a TUI, and each Send retries the dial.
type DiagSender struct {
	mu   sync.Mutex
	cfg  Config
	conn net.Conn
}

// NewDiagSender returns a sender for cfg (connecting lazily).
func NewDiagSender(cfg Config) *DiagSender { return &DiagSender{cfg: cfg} }

// Send writes one batch of crash records, reconnecting once if the listener went
// away.
func (d *DiagSender) Send(errs []TraceError) {
	if len(errs) == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	line, err := json.Marshal(diagEnvelope{Cadr: "diag", Errors: errs})
	if err != nil {
		return
	}
	line = append(line, '\n')
	for attempt := 0; attempt < 2; attempt++ {
		if d.conn == nil {
			conn, derr := dialConfig(d.cfg)
			if derr != nil {
				return
			}
			d.conn = conn
		}
		if _, werr := d.conn.Write(line); werr == nil {
			return
		}
		_ = d.conn.Close()
		d.conn = nil
	}
}

// Close releases the connection.
func (d *DiagSender) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn != nil {
		_ = d.conn.Close()
		d.conn = nil
	}
}

func dialConfig(cfg Config) (net.Conn, error) {
	if cfg.Protocol == "tcp" {
		port := cfg.Port
		if port == "" {
			port = "9876"
		}
		return net.Dial("tcp", "localhost:"+port)
	}
	return net.Dial("unix", cfg.Socket)
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
	return StartListenerWithRootDiag(root, onEvent, nil)
}

// StartListenerWithRootDiag listens for events and forwarded crash records.
func StartListenerWithRootDiag(root string, onEvent func(Event), onDiag func([]TraceError)) (*Listener, error) {
	cfg := LoadConfig(root)
	return StartListenerWithDiag(cfg, onEvent, onDiag)
}

func StartListenerWithConfig(cfg Config, onEvent func(Event)) (*Listener, error) {
	return StartListenerWithDiag(cfg, onEvent, nil)
}

// StartListenerWithDiag is StartListenerWithConfig plus a sink for forwarded crash
// records (`cadr run` sends them over the same socket as events).
func StartListenerWithDiag(cfg Config, onEvent func(Event), onDiag func([]TraceError)) (*Listener, error) {
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
					line := scanner.Bytes()
					if errs, ok := ParseDiagLine(line); ok {
						if onDiag != nil {
							onDiag(errs)
						}
						continue
					}
					if e, ok := ParseEventLine(line); ok {
						onEvent(e)
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
