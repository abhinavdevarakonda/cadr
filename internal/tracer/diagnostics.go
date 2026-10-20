package tracer

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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

		if m := reGoPanic.FindStringSubmatch(trimmed); m != nil {
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
				if i := strings.Index(trimmed, "("); i > 0 {
					name := strings.TrimPrefix(strings.TrimSpace(trimmed[:i]), "created by ")
					lastFn = strings.TrimSuffix(name, " in goroutine")
					continue
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

// fillHead copies the failing frame into the error's Fn/File/Line. Go prints the
// innermost frame first; Python's "most recent call last" prints it last.
func fillHead(e *TraceError) {
	if len(e.Frames) == 0 || e.File != "" {
		return
	}
	f := e.Frames[0]
	if e.Kind == ErrException {
		f = e.Frames[len(e.Frames)-1]
	}
	e.Fn, e.File, e.Line = f.Fn, f.File, f.Line
}

// LooksLikeCrash reports whether a stderr line starts a crash block, so a caller
// can re-parse its buffered diagnostics as the rest of the block arrives.
func LooksLikeCrash(line string) bool {
	trimmed := strings.TrimSpace(line)
	return reGoPanic.MatchString(trimmed) || rePyHeader.MatchString(trimmed)
}

// Localize resolves each error's failing frame to a function from funcs (the
// static graph's function ranges), so CLI, TUI and MCP can name the function that
// broke without the tracer holding a graph.
func Localize(errs []TraceError, funcs []StaticFunc) []TraceError {
	if len(funcs) == 0 {
		return errs
	}
	for i := range errs {
		if errs[i].Fn != "" {
			continue
		}
		for _, f := range errs[i].Frames {
			if name, ok := LookupFunc(funcs, f.File, f.Line); ok {
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

// LookupFunc returns the function whose source range contains file:line (the
// innermost one when ranges nest), falling back to the nearest declaration at or
// above the line when ranges are unknown.
func LookupFunc(funcs []StaticFunc, file string, line int) (string, bool) {
	containName, containLine := "", -1
	bestName, bestLine := "", -1
	for _, f := range funcs {
		if f.File != file || f.Line == 0 || f.Line > line {
			continue
		}
		if f.EndLine >= line && f.Line > containLine {
			containName, containLine = f.Name, f.Line
		}
		if f.Line > bestLine {
			bestName, bestLine = f.Name, f.Line
		}
	}
	if containName != "" {
		return containName, true
	}
	return bestName, bestName != ""
}

// Diagnostics collects the child's stderr lines that were not trace events. It is
// bounded so a chatty program cannot grow a run record without limit.
type Diagnostics struct {
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
	return append([]string(nil), d.lines...)
}

// Errors parses the retained lines into crash records.
func (d *Diagnostics) Errors() []TraceError {
	return ParseStderr(d.lines)
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
	rel, err := filepath.Rel(root, file)
	if err != nil || strings.HasPrefix(rel, "..") {
		return file
	}
	return filepath.ToSlash(rel)
}
