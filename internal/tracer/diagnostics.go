package tracer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Spot error kinds, matching how the runtime reported the failure.
const (
	ErrPanic     = "panic"     // Go panic
	ErrFatal     = "fatal"     // Go fatal error (unrecoverable runtime fault)
	ErrException = "exception" // Python uncaught exception
)

// DefaultDiagLines is how many stderr lines are retained per run.
const DefaultDiagLines = 100

// Frame is one stack-frame entry reported by the crashed process.
type Frame struct {
	Fn   string `json:"fn,omitempty"`
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
}

// TraceError is a failure that ended the traced run, recovered from the child's
// stderr. The tracer has no static graph, so function names are resolved later by
// consumers calling Localize; Fn/File/Line hold that resolved result.
type TraceError struct {
	Kind    string  `json:"kind"`
	Message string  `json:"message,omitempty"`
	Fn      string  `json:"fn,omitempty"`
	File    string  `json:"file,omitempty"`
	Line    int     `json:"line,omitempty"`
	Frames  []Frame `json:"frames,omitempty"`
}

var (
	reGoPanic     = regexp.MustCompile(`^(panic|fatal error):[ \t]*(.*)$`)
	reGoGoroutine = regexp.MustCompile(`^goroutine \d+ \[`)
	reGoFrame     = regexp.MustCompile(`^(.*\.go):(\d+)(?:[ \t]+\+0x[0-9a-fA-F]+)?$`)
	rePyHeader    = regexp.MustCompile(`^Traceback \(most recent call last\):$`)
	rePyFrame     = regexp.MustCompile(`^[ \t]+File "(.+)", line (\d+), in (.+)$`)
	// net/http recovers a panicking handler and logs `http: panic serving
	// <addr>: <message>`; the process keeps running, so this is the only signal.
	reGoHTTPPanic = regexp.MustCompile(`^http: panic serving \S+: (.*)$`)
	// The standard logger prefixes that line with a timestamp.
	reLogPrefix = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?\s+`)
)

// ParseStderr turns the retained stderr lines into crash records. It recognises
// Go panic/fatal-error blocks (using only the first goroutine block, which is the
// failing one) and Python tracebacks. Lines that do not belong to a crash block
// are ignored, so ordinary log output yields no errors.
func ParseStderr(lines []string) []TraceError {
	var out []TraceError
	var cur *TraceError
	lastFn := ""
	goBlock := 0 // 0: no header yet, 1: inside the panic's block, 2: finished

	add := func(kind, msg string) *TraceError {
		for i := range out {
			if out[i].Kind == kind && out[i].Message == msg {
				return nil
			}
		}
		out = append(out, TraceError{Kind: kind, Message: msg})
		return &out[len(out)-1]
	}

	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r\n")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		bare := strings.TrimSpace(reLogPrefix.ReplaceAllString(trimmed, ""))

		if m := reGoHTTPPanic.FindStringSubmatch(bare); m != nil {
			cur = add(ErrPanic, "panic: "+m[1])
			lastFn, goBlock = "", 0
			continue
		}
		if m := reGoPanic.FindStringSubmatch(bare); m != nil {
			kind := ErrPanic
			if m[1] == "fatal error" {
				kind = ErrFatal
			}
			cur = add(kind, trimmed)
			lastFn, goBlock = "", 0
			continue
		}
		if rePyHeader.MatchString(trimmed) {
			cur = add(ErrException, "")
			lastFn, goBlock = "", 0
			continue
		}
		if cur == nil {
			continue
		}

		if reGoGoroutine.MatchString(trimmed) {
			if goBlock >= 1 {
				// Later goroutine blocks are unrelated stacks.
				goBlock = 2
				cur = nil
				continue
			}
			goBlock = 1
			continue
		}

		if m := rePyFrame.FindStringSubmatch(line); m != nil {
			ln, _ := strconv.Atoi(m[2])
			cur.Frames = append(cur.Frames, Frame{Fn: strings.TrimSpace(m[3]), File: m[1], Line: ln})
			continue
		}

		if goBlock == 1 {
			// An unindented line inside the block names the next frame
			// (`pkg.Fn(...)`); file lines are indented with a tab.
			if !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, " ") {
				if name := funcNameBeforeArgs(trimmed); name != "" {
					name = strings.TrimPrefix(name, "created by ")
					lastFn = strings.TrimSuffix(name, " in goroutine")
				}
				continue
			}
			if m := reGoFrame.FindStringSubmatch(trimmed); m != nil {
				ln, _ := strconv.Atoi(m[2])
				cur.Frames = append(cur.Frames, Frame{Fn: lastFn, File: m[1], Line: ln})
				lastFn = ""
			}
			continue
		}

		// Python: the first unindented line after the frames is the exception.
		if cur.Kind == ErrException && cur.Message == "" &&
			!strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			cur.Message = trimmed
		}
	}

	for i := range out {
		fillHead(&out[i])
	}
	return out
}

// funcNameBeforeArgs returns the function part of a stack line such as
// "main.boom({0x1, 0x2}, 0x3)" or "net/http.(*conn).serve.func1()". A '(' directly
// after a '.' belongs to a receiver like (*conn), not to the argument list.
func funcNameBeforeArgs(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] != '(' {
			continue
		}
		if i > 0 && line[i-1] == '.' {
			continue
		}
		return strings.TrimSpace(line[:i])
	}
	return ""
}

// fillHead copies the failing frame into the error's Fn/File/Line. Go prints the
// innermost frame first; Python's "most recent call last" prints it last.
func fillHead(e *TraceError) {
	if len(e.Frames) == 0 || e.File != "" {
		return
	}
	f := e.Frames[0]
	if e.Kind == ErrException {
		f = e.Frames[len(e.Frames)-1]
	} else {
		// A recovered handler panic is reported with net/http and runtime frames
		// first; the failing application frame is the one that matters.
		for _, cand := range e.Frames {
			if !isStdlibFrame(cand) {
				f = cand
				break
			}
		}
	}
	e.Fn, e.File, e.Line = f.Fn, f.File, f.Line
}

// isStdlibFrame reports whether a frame belongs to the Go toolchain or runtime
// rather than the traced application.
func isStdlibFrame(f Frame) bool {
	p := filepath.ToSlash(f.File)
	if strings.Contains(p, "/libexec/src/") || strings.Contains(p, "/go/src/") ||
		strings.Contains(p, "/usr/local/go/") || strings.Contains(p, "/usr/lib/go/") {
		return true
	}
	if root := filepath.ToSlash(os.Getenv("GOROOT")); root != "" && strings.HasPrefix(p, root+"/src/") {
		return true
	}
	return f.Fn == "panic" || strings.HasPrefix(f.Fn, "panic(") || strings.HasPrefix(f.Fn, "runtime.")
}

// LooksLikeCrash reports whether a stderr line starts a crash block, so a caller
// can re-parse its buffered diagnostics as the rest of the block arrives.
func LooksLikeCrash(line string) bool {
	trimmed := strings.TrimSpace(line)
	bare := strings.TrimSpace(reLogPrefix.ReplaceAllString(trimmed, ""))
	return reGoPanic.MatchString(bare) || reGoHTTPPanic.MatchString(bare) || rePyHeader.MatchString(bare)
}

// Localize resolves each error's failing frame to a function, so CLI, TUI and MCP can
// name the function that broke without the tracer holding a graph.
func Localize(errs []TraceError, funcs []StaticFunc) []TraceError {
	if len(funcs) == 0 {
		return errs
	}
	for i := range errs {
		if errs[i].Fn != "" {
			continue
		}
		for _, f := range errs[i].Frames {
			if name, ok := LookupContaining(funcs, f.File, f.Line); ok {
				errs[i].Fn = name
				if errs[i].File == "" {
					errs[i].File, errs[i].Line = f.File, f.Line
				}
				break
			}
		}
	}
	return errs
}

// LookupContaining returns the function whose source range contains file:line, the
// innermost one when ranges nest. There is deliberately no "nearest declaration
// above the line" fallback: a line that no range contains (module-level code,
// generated code, a file the graph does not know) belongs to no function, and
// guessing a caller would put the error on the wrong row.
func LookupContaining(funcs []StaticFunc, file string, line int) (string, bool) {
	name, start := "", -1
	for _, f := range funcs {
		if f.Line == 0 || f.EndLine < line || f.Line > line || !pathMatches(f.File, file) {
			continue
		}
		if f.Line > start {
			name, start = pickName(f), f.Line
		}
	}
	return name, name != ""
}

// pickName returns the value a caller should use as the tree key.
func pickName(sf StaticFunc) string {
	if sf.ID != "" {
		return sf.ID
	}
	return sf.Name
}

// pathMatches compares a graph path with a runtime path, tolerating the
// relative-vs-absolute difference and a leading "./".
func pathMatches(graphPath, stackPath string) bool {
	if graphPath == "" || stackPath == "" {
		return false
	}
	if graphPath == stackPath {
		return true
	}
	g := filepath.ToSlash(graphPath)
	s := filepath.ToSlash(stackPath)
	if strings.HasSuffix(s, "/"+strings.TrimPrefix(g, "./")) {
		return true
	}
	if strings.HasSuffix(g, "/"+strings.TrimPrefix(s, "./")) {
		return true
	}
	ga, err1 := filepath.Abs(g)
	sa, err2 := filepath.Abs(s)
	return err1 == nil && err2 == nil && ga == sa
}

// Diagnostics collects the child's stderr lines that were not trace events. It is
// bounded so a chatty program cannot grow a run record without limit.
type Diagnostics struct {
	mu       sync.Mutex
	maxLines int
	lines    []string
}

// NewDiagnostics returns a collector retaining the last DefaultDiagLines lines.
func NewDiagnostics() *Diagnostics {
	return &Diagnostics{maxLines: DefaultDiagLines}
}

// Add records one non-event stderr line. Blank lines and cadr's own hook banner
// ("cadr: Connected to monitor.") are dropped: the buffer is the *program's*
// output, not the tracer's.
func (d *Diagnostics) Add(line string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	line = strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "cadr: ") {
		return
	}
	d.lines = append(d.lines, line)
	if len(d.lines) > d.maxLines {
		d.lines = append([]string(nil), d.lines[len(d.lines)-d.maxLines:]...)
	}
}

// Lines returns a copy of the retained lines, oldest first.
func (d *Diagnostics) Lines() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.lines...)
}

// Errors parses the retained lines into crash records. It is safe to call
// concurrently with Add, which matters because a Ctrl-C finalizes a run while the
// tracer is still reading the child's stderr.
func (d *Diagnostics) Errors() []TraceError {
	d.mu.Lock()
	lines := append([]string(nil), d.lines...)
	d.mu.Unlock()
	return ParseStderr(lines)
}

// FormatErrors renders crash records for humans, trimming root from file paths so
// a trace of the analyzed project shows project-relative locations. It returns ""
// when there is nothing to show, so callers can append it unconditionally.
func FormatErrors(errs []TraceError, root string) string {
	if len(errs) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "errors (%d):\n", len(errs))
	for _, e := range errs {
		file := shortPath(e.File, root)
		var loc string
		switch {
		case e.Fn != "" && file != "":
			loc = fmt.Sprintf("%s  %s:%d", e.Fn, file, e.Line)
		case e.Fn != "":
			loc = e.Fn
		case file != "":
			loc = fmt.Sprintf("%s:%d", file, e.Line)
		}
		if loc != "" {
			fmt.Fprintf(&b, "  %-9s %s\n", e.Kind, loc)
		} else {
			fmt.Fprintf(&b, "  %s\n", e.Kind)
		}
		msg := e.Message
		if msg == "" {
			msg = "(no message)"
		}
		fmt.Fprintf(&b, "            %s\n", msg)
	}
	return b.String()
}

// shortPath makes a trace's absolute source path project-relative when root is a
// prefix of it.
func shortPath(file, root string) string {
	if file == "" {
		return ""
	}
	if root == "" {
		return file
	}
	// The stack path is absolute while the project root is usually "." — compare
	// absolute to absolute. Symlinks are resolved too, so /tmp and /private/tmp
	// (macOS) do not defeat the trim.
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return file
	}
	roots := []string{absRoot}
	if resolved, rerr := filepath.EvalSymlinks(absRoot); rerr == nil && resolved != absRoot {
		roots = append(roots, resolved)
	}
	absFile := file
	if resolved, rerr := filepath.EvalSymlinks(filepath.Dir(file)); rerr == nil {
		absFile = filepath.Join(resolved, filepath.Base(file))
	}
	for _, r := range roots {
		if rel, rerr := filepath.Rel(r, absFile); rerr == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return file
}
