package tracer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseStderrGoPanic(t *testing.T) {
	lines := []string{
		"panic: runtime error: index out of range [7] with length 3",
		"",
		"goroutine 1 [running]:",
		"main.handler(...)",
		"\t/home/u/app/handlers/books.go:42 +0x1a5",
		"main.main()",
		"\t/home/u/app/main.go:10 +0x25",
		"",
		"goroutine 6 [chan receive]:",
		"main.worker(...)",
		"\t/home/u/app/worker.go:9",
	}
	errs := ParseStderr(lines)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %+v", len(errs), errs)
	}
	e := errs[0]
	if e.Kind != ErrPanic {
		t.Fatalf("kind = %q, want panic", e.Kind)
	}
	if e.Message != "panic: runtime error: index out of range [7] with length 3" {
		t.Fatalf("message = %q", e.Message)
	}
	// Only the failing goroutine's block is used.
	if len(e.Frames) != 2 {
		t.Fatalf("frames = %d, want 2: %+v", len(e.Frames), e.Frames)
	}
	if e.Frames[0].Fn != "main.handler" || e.Frames[0].Line != 42 || e.Frames[0].File != "/home/u/app/handlers/books.go" {
		t.Fatalf("frame 0 = %+v", e.Frames[0])
	}
	if e.Fn != "main.handler" || e.Line != 42 || e.File != "/home/u/app/handlers/books.go" {
		t.Fatalf("head = %s %s:%d", e.Fn, e.File, e.Line)
	}
}

func TestParseStderrGoFatal(t *testing.T) {
	lines := []string{
		"fatal error: concurrent map writes",
		"",
		"goroutine 20 [running]:",
		"main.mutate(...)",
		"\t/app/store.go:88",
	}
	errs := ParseStderr(lines)
	if len(errs) != 1 || errs[0].Kind != ErrFatal {
		t.Fatalf("errors = %+v", errs)
	}
	if errs[0].Fn != "main.mutate" || errs[0].Line != 88 {
		t.Fatalf("head = %s:%d", errs[0].Fn, errs[0].Line)
	}
}

func TestParseStderrPythonTraceback(t *testing.T) {
	lines := []string{
		"Traceback (most recent call last):",
		`  File "/app/main.py", line 10, in <module>`,
		"    app.run()",
		`  File "/app/app.py", line 42, in run`,
		`    raise ValueError("bad input")`,
		"ValueError: bad input",
	}
	errs := ParseStderr(lines)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %+v", len(errs), errs)
	}
	e := errs[0]
	if e.Kind != ErrException {
		t.Fatalf("kind = %q", e.Kind)
	}
	if e.Message != "ValueError: bad input" {
		t.Fatalf("message = %q", e.Message)
	}
	if len(e.Frames) != 2 {
		t.Fatalf("frames = %d, want 2", len(e.Frames))
	}
	// Python prints the innermost frame last, so the head is the last frame.
	if e.Fn != "run" || e.File != "/app/app.py" || e.Line != 42 {
		t.Fatalf("head = %s %s:%d", e.Fn, e.File, e.Line)
	}
}

// Ordinary log output must never be reported as a crash.
func TestParseStderrNoiseIsNotAnError(t *testing.T) {
	lines := []string{
		"2026/10/14 15:33:01 listening on :8080",
		"level=info msg=\"GET /books\" status=200",
		"WARNING: something looked odd but the process kept going",
		"Error: this is a handled application error, not a crash",
		"[GIN] 2026/10/14 - 15:33:02 | 200 | 1.2ms | 127.0.0.1 | GET /books",
	}
	if errs := ParseStderr(lines); len(errs) != 0 {
		t.Fatalf("noise produced errors: %+v", errs)
	}
}

func TestParseStderrDeduplicates(t *testing.T) {
	block := []string{
		"panic: boom",
		"goroutine 1 [running]:",
		"main.f(...)",
		"\t/app/a.go:7",
	}
	lines := append(append([]string{}, block...), block...)
	if errs := ParseStderr(lines); len(errs) != 1 {
		t.Fatalf("got %d errors, want 1 duplicate collapsed: %+v", len(errs), errs)
	}
}

func TestLocalize(t *testing.T) {
	funcs := []StaticFunc{
		{Name: "main.handler", File: "/app/handlers.go", Line: 40, EndLine: 60},
		{Name: "main.inner", File: "/app/handlers.go", Line: 45, EndLine: 50},
	}
	errs := []TraceError{
		{Frames: []Frame{{File: "/app/handlers.go", Line: 48}}},
	}
	got := Localize(errs, funcs)
	if got[0].Fn != "main.inner" {
		t.Fatalf("Fn = %q, want the innermost containing function", got[0].Fn)
	}
	if got[0].File != "/app/handlers.go" || got[0].Line != 48 {
		t.Fatalf("head = %s:%d", got[0].File, got[0].Line)
	}
}

// A frame outside every known range is left unresolved rather than attributed to
// the nearest declaration above it.
func TestLocalizeDoesNotGuessNearestAbove(t *testing.T) {
	funcs := []StaticFunc{{Name: "main.handler", File: "/app/handlers.go", Line: 40, EndLine: 60}}
	errs := []TraceError{{Frames: []Frame{{File: "/app/handlers.go", Line: 72}}}}
	if got := Localize(errs, funcs); got[0].Fn != "" {
		t.Fatalf("Fn = %q, want empty (no enclosing function)", got[0].Fn)
	}
}

func TestDiagnosticsRingIsBounded(t *testing.T) {
	d := NewDiagnostics()
	for i := 0; i < DefaultDiagLines+50; i++ {
		d.Add("line")
	}
	if got := len(d.Lines()); got != DefaultDiagLines {
		t.Fatalf("retained %d lines, want %d", got, DefaultDiagLines)
	}
	d.Add("   ")
	if got := len(d.Lines()); got != DefaultDiagLines {
		t.Fatalf("blank line should be ignored, retained %d", got)
	}
}

// cadr's own hook banner is not the program's output and must not enter the tail.
func TestDiagnosticsIgnoresCadrBanner(t *testing.T) {
	d := NewDiagnostics()
	d.Add("cadr: Connected to monitor.")
	d.Add("real program output")
	if got := d.Lines(); len(got) != 1 || got[0] != "real program output" {
		t.Fatalf("lines = %v", got)
	}
}

// The graph stores paths relative to the analyzed root while a runtime stack
// reports absolute paths. Containment must still match across that difference.
func TestLookupContainingAcrossPaths(t *testing.T) {
	funcs := []StaticFunc{
		{ID: "main.crash", Name: "crash", File: "internal/app/main.go", Line: 6, EndLine: 9},
		{ID: "main.main", Name: "main", File: "internal/app/main.go", Line: 16, EndLine: 19},
	}
	const abs = "/Users/me/proj/internal/app/main.go"

	if id, ok := LookupContaining(funcs, abs, 8); !ok || id != "main.crash" {
		t.Fatalf("line 8: got %q ok=%v, want main.crash", id, ok)
	}
	if id, ok := LookupContaining(funcs, abs, 18); !ok || id != "main.main" {
		t.Fatalf("line 18: got %q ok=%v, want main.main", id, ok)
	}
	// A module-level line belongs to no function: no nearest-above guess.
	if id, ok := LookupContaining(funcs, abs, 25); ok {
		t.Fatalf("line 25 resolved to %q, want no function", id)
	}
}

// Nested ranges resolve to the innermost function.
func TestLookupContainingInnermost(t *testing.T) {
	funcs := []StaticFunc{
		{ID: "outer", File: "/p/a.go", Line: 1, EndLine: 40},
		{ID: "inner", File: "/p/a.go", Line: 10, EndLine: 20},
	}
	if id, ok := LookupContaining(funcs, "/p/a.go", 15); !ok || id != "inner" {
		t.Fatalf("got %q ok=%v, want inner", id, ok)
	}
}

// A graph without end lines cannot answer by range, and must not guess.
func TestLookupContainingRejectsUnknownRanges(t *testing.T) {
	funcs := []StaticFunc{{ID: "f", File: "/p/a.go", Line: 1}}
	if id, ok := LookupContaining(funcs, "/p/a.go", 30); ok {
		t.Fatalf("resolved %q with no EndLine, want no function", id)
	}
}

func TestShortPathTrimsAbsoluteRoot(t *testing.T) {
	root := t.TempDir()
	got := shortPath(filepath.Join(root, "internal", "app", "main.go"), root)
	if got != "internal/app/main.go" {
		t.Fatalf("shortPath = %q", got)
	}
}

// net/http recovers a panicking handler and logs `http: panic serving <addr>:
// <msg>` with a log timestamp. The process keeps running, so this line is the only
// signal, and the reported stack starts with net/http/runtime frames.
func TestParseStderrHTTPHandlerPanic(t *testing.T) {
	lines := []string{
		"2026/10/22 20:44:09 http: panic serving 127.0.0.1:52641: runtime error: index out of range [3] with length 0",
		"goroutine 20 [running]:",
		"net/http.(*conn).serve.func1()",
		"\t/opt/homebrew/Cellar/go/1.27.2/libexec/src/net/http/server.go:1959 +0xac",
		"panic({0x100df6798?, 0xee9d69c150?})",
		"\t/opt/homebrew/Cellar/go/1.27.2/libexec/src/runtime/panic.go:859 +0x120",
		"main.boom({0xee9d686170?, 0xee9d69c13c?}, 0xee9d6d9a98?)",
		"\t/Users/me/proj/handlers.go:10 +0x1c",
		"net/http.HandlerFunc.ServeHTTP(0x100e57540?, {0x100e09960?, 0xee9d6c80f0?}, 0x100b08270?)",
		"\t/opt/homebrew/Cellar/go/1.27.2/libexec/src/net/http/server.go:2358 +0x38",
	}
	errs := ParseStderr(lines)
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %+v", len(errs), errs)
	}
	e := errs[0]
	if e.Kind != ErrPanic {
		t.Fatalf("kind = %q, want panic", e.Kind)
	}
	if !strings.Contains(e.Message, "index out of range") {
		t.Fatalf("message = %q", e.Message)
	}
	// The head must be the application frame, not net/http or runtime.
	if e.Fn != "main.boom" || e.Line != 10 || !strings.HasSuffix(e.File, "handlers.go") {
		t.Fatalf("head = %s %s:%d, want main.boom handlers.go:10", e.Fn, e.File, e.Line)
	}
	// Repeated handler panics must collapse even though the ephemeral port differs.
	again := append([]string{"2026/10/22 20:45:00 http: panic serving 127.0.0.1:52000: runtime error: index out of range [3] with length 0"}, lines[1:]...)
	if got := ParseStderr(append(append([]string{}, lines...), again...)); len(got) != 1 {
		t.Fatalf("duplicate handler panics not collapsed: %+v", got)
	}
}

func TestLooksLikeCrashHTTPPanic(t *testing.T) {
	line := "2026/10/22 20:44:09 http: panic serving 127.0.0.1:52641: runtime error: boom"
	if !LooksLikeCrash(line) {
		t.Fatal("LooksLikeCrash must fire for a recovered handler panic")
	}
}

// Receiver parentheses must not truncate a frame's function name.
func TestFuncNameBeforeArgs(t *testing.T) {
	cases := map[string]string{
		"net/http.(*conn).serve.func1()":                     "net/http.(*conn).serve.func1",
		"main.boom({0x1?, 0x2?}, 0x3?)":                      "main.boom",
		"panic({0x100df6798?, 0xee9d69c150?})":               "panic",
		"net/http.HandlerFunc.ServeHTTP(0x1?, {0x2?}, 0x3?)": "net/http.HandlerFunc.ServeHTTP",
		"created by net/http.(*Server).Serve in goroutine 1": "",
	}
	for in, want := range cases {
		if got := funcNameBeforeArgs(in); got != want {
			t.Fatalf("funcNameBeforeArgs(%q) = %q, want %q", in, got, want)
		}
	}
}

// A crash seen by `cadr run` must reach a TUI listening on the socket: the TUI only
// receives events otherwise, so it could never mark a failure.
func TestDiagnosticsTravelOverTheTransport(t *testing.T) {
	// Unix socket paths are limited to ~104 bytes, so keep it short.
	sock := filepath.Join(os.TempDir(), "cadr-diagtest.sock")
	_ = os.Remove(sock)
	defer os.Remove(sock)
	cfg := Config{Protocol: "uds", Socket: sock}

	got := make(chan []TraceError, 4)
	ln, err := StartListenerWithDiag(cfg, func(Event) {}, func(errs []TraceError) { got <- errs })
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	SendDiagnostics(cfg, []TraceError{{
		Kind:    ErrException,
		Message: "NameError: name 'x' is not defined",
		Fn:      "_read_lines",
		File:    "/app/helpers/read_db.py",
		Line:    5,
	}})

	select {
	case errs := <-got:
		if len(errs) != 1 || errs[0].Kind != ErrException || errs[0].Fn != "_read_lines" || errs[0].Line != 5 {
			t.Fatalf("received %+v", errs)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no diagnostics arrived over the transport")
	}
}

// A forwarded crash record must never be mistaken for a trace event.
func TestParseDiagLineIsNotAnEvent(t *testing.T) {
	line := []byte(`{"cadr":"diag","errors":[{"kind":"exception","message":"boom"}]}`)
	if _, ok := ParseEventLine(line); ok {
		t.Fatal("a diag line was classified as a trace event")
	}
	errs, ok := ParseDiagLine(line)
	if !ok || len(errs) != 1 || errs[0].Kind != ErrException {
		t.Fatalf("ParseDiagLine = %+v ok=%v", errs, ok)
	}
}
