package tracer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/abhinavdevarakonda/cadr/internal/agents"
	"github.com/abhinavdevarakonda/cadr/internal/toolwrap"
)

// goTraceVerbs are the go subcommands that actually execute the compiled
// program and are therefore worth instrumenting in v1.
var goTraceVerbs = map[string]bool{"run": true, "test": true}

// goVerbList is the set of known go subcommands, used to locate the verb when
// global flags precede it.
var goVerbList = map[string]bool{
	"build": true, "run": true, "test": true, "install": true, "list": true,
	"vet": true, "generate": true, "work": true, "clean": true, "env": true,
	"bug": true, "doc": true, "fmt": true, "fix": true, "get": true,
	"mod": true, "telemetry": true, "tool": true, "version": true,
}

func runGoCmd(fullCmd string, localOnly bool, onEvent func(Event)) error {
	parts := agents.SplitCommand(fullCmd)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	env := os.Environ()
	if goVerbNeedsTrace(parts) {
		if bad := goInstrumentUnsupported(parts); bad != "" {
			// The pre-built runtime import fragment does not match the
			// instrumented standard library, so tracing is skipped rather than
			// failing the user's build.
			fmt.Fprintf(os.Stderr, "cadr: go tracing disabled for %s builds\n", bad)
		} else if goEnv, ok := toolwrap.PrepareEnv("."); ok {
			env = goEnv
			parts = injectToolexec(parts)
		}
	}

	if localOnly {
		env = append(env, "CADR_LOCAL_ONLY=1")
	}

	cfg := LoadConfig(".")
	if cfg.Protocol == "tcp" {
		env = append(env, "CADR_TCP="+cfg.Port)
	} else {
		env = append(env, "CADR_SOCKET="+cfg.Socket)
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Env = env
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
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			fmt.Fprintln(os.Stderr, line)
			continue
		}
		onEvent(event)
	}

	return cmd.Wait()
}

// goInstrumentUnsupportedFlags are build modes whose standard library differs
// from the fragment built by toolwrap.PrepareEnv, so -toolexec injection is
// skipped to avoid a link-time fingerprint mismatch.
var goInstrumentUnsupportedFlags = []string{"-race", "-msan", "-asan"}

func goInstrumentUnsupported(parts []string) string {
	for _, a := range parts {
		for _, f := range goInstrumentUnsupportedFlags {
			if a == f || strings.HasPrefix(a, f+"=") {
				return f
			}
		}
	}
	return ""
}

// goVerbNeedsTrace reports whether the command is a go invocation that will
// execute user code under the injected toolchain.
func goVerbNeedsTrace(parts []string) bool {
	if len(parts) < 2 {
		return false
	}
	for i := 1; i < len(parts); i++ {
		if goVerbList[parts[i]] {
			return goTraceVerbs[parts[i]]
		}
	}
	return false
}

// injectToolexec inserts -toolexec after the go verb unless the user already
// supplied one.
func injectToolexec(parts []string) []string {
	verbIdx := -1
	for i := 1; i < len(parts); i++ {
		if goVerbList[parts[i]] {
			verbIdx = i
			break
		}
	}
	if verbIdx == -1 {
		return parts
	}
	for _, a := range parts {
		if a == "-toolexec" || strings.HasPrefix(a, "-toolexec=") {
			return parts
		}
	}

	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = os.Args[0]
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}

	out := make([]string, 0, len(parts)+1)
	out = append(out, parts[:verbIdx+1]...)
	out = append(out, "-toolexec="+toolwrap.ToolexecValue(exe))
	out = append(out, parts[verbIdx+1:]...)
	return out
}
