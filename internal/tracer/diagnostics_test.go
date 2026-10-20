package tracer

import "testing"

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

// With unknown ranges, localization falls back to the nearest declaration above
// the failing line.
func TestLocalizeFallsBackToNearest(t *testing.T) {
	funcs := []StaticFunc{{Name: "main.handler", File: "/app/handlers.go", Line: 40}}
	errs := []TraceError{{Frames: []Frame{{File: "/app/handlers.go", Line: 72}}}}
	if got := Localize(errs, funcs); got[0].Fn != "main.handler" {
		t.Fatalf("Fn = %q, want nearest preceding", got[0].Fn)
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
