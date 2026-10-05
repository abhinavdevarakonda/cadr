package tracer

import (
	"strings"
	"testing"
)

func TestGoVerbNeedsTrace(t *testing.T) {
	trace := [][]string{
		{"go", "run", "."},
		{"go", "test", "./..."},
		{"go", "-C", "/proj", "run", "."},
	}
	for _, parts := range trace {
		if !goVerbNeedsTrace(parts) {
			t.Errorf("%v: expected trace", parts)
		}
	}
	noTrace := [][]string{
		{"go", "build", "./..."},
		{"go", "install", "."},
		{"go", "vet", "./..."},
		{"go"},
	}
	for _, parts := range noTrace {
		if goVerbNeedsTrace(parts) {
			t.Errorf("%v: expected no trace", parts)
		}
	}
}

func TestGoInstrumentUnsupported(t *testing.T) {
	cases := []struct {
		parts []string
		want  string
	}{
		{[]string{"go", "test", "-race", "./..."}, "-race"},
		{[]string{"go", "build", "-msan", "."}, "-msan"},
		{[]string{"go", "test", "./..."}, ""},
	}
	for _, c := range cases {
		if got := goInstrumentUnsupported(c.parts); got != c.want {
			t.Errorf("%v: got %q want %q", c.parts, got, c.want)
		}
	}
}

func TestInjectToolexec(t *testing.T) {
	got := injectToolexec([]string{"go", "run", "."})
	if len(got) != 4 || !strings.HasPrefix(got[2], "-toolexec=") {
		t.Fatalf("got %v", got)
	}
	if !strings.HasSuffix(got[2], " tool-wrap") {
		t.Fatalf("spec missing tool-wrap: %q", got[2])
	}
	if got[3] != "." {
		t.Fatalf("arg order changed: %v", got)
	}

	// Respect an explicit user -toolexec.
	parts := []string{"go", "run", "-toolexec=other", "."}
	if got := injectToolexec(parts); len(got) != 4 || got[2] != "-toolexec=other" {
		t.Fatalf("user toolexec overwritten: %v", got)
	}

	// Inserts after the verb, before verb flags.
	got = injectToolexec([]string{"go", "test", "-run", "X", "./..."})
	if !strings.HasPrefix(got[2], "-toolexec=") || got[3] != "-run" {
		t.Fatalf("flag order wrong: %v", got)
	}
}
