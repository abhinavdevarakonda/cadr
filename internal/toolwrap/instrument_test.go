package toolwrap

import (
	"strings"
	"testing"
)

func lineCount(s string) int { return strings.Count(s, "\n") }

func callCount(s string) int { return strings.Count(s, "__cadr_trace(") }

func TestInstrumentSource(t *testing.T) {
	root := "/proj"
	path := "/proj/pkg/app.go"

	tests := []struct {
		name      string
		src       string
		wantCalls []string // exact __cadr_trace call prefixes
		wantCount int
		wantPkg   string
	}{
		{
			name: "plain function",
			src:  "package app\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n",
			wantCalls: []string{
				`__cadr_trace("Add","/proj/pkg/app.go",3);`,
			},
			wantCount: 1,
			wantPkg:   "app",
		},
		{
			name: "methods value pointer and generic receiver",
			src:  "package app\n\ntype Stack[T any] struct{}\n\nfunc (s Stack[T]) Len() int { return 0 }\n\nfunc (s *Stack[T]) Push(v T) {}\n\nfunc (s *Stack[T]) pop() {}\n",
			wantCalls: []string{
				`__cadr_trace("Stack.Len","/proj/pkg/app.go",5);`,
				`__cadr_trace("Stack.Push","/proj/pkg/app.go",7);`,
				`__cadr_trace("Stack.pop","/proj/pkg/app.go",9);`,
			},
			wantCount: 3,
			wantPkg:   "app",
		},
		{
			name: "multi-line signature",
			src:  "package app\n\nfunc Long(\n\ta int,\n\tb int,\n) int {\n\treturn a + b\n}\n",
			wantCalls: []string{
				`__cadr_trace("Long","/proj/pkg/app.go",3);`,
			},
			wantCount: 1,
			wantPkg:   "app",
		},
		{
			name: "one-line and empty bodies",
			src:  "package app\n\nfunc One() { One() }\n\nfunc Empty() {}\n",
			wantCalls: []string{
				`__cadr_trace("One","/proj/pkg/app.go",3);`,
				`__cadr_trace("Empty","/proj/pkg/app.go",5);`,
			},
			wantCount: 2,
			wantPkg:   "app",
		},
		{
			name: "init underscore and bodyless skipped",
			src:  "package app\n\nfunc init() {}\n\nfunc _() {}\n\nfunc Asm()\n\nfunc Keep() {}\n",
			wantCalls: []string{
				`__cadr_trace("Keep","/proj/pkg/app.go",9);`,
			},
			wantCount: 1,
			wantPkg:   "app",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, n, pkg, err := instrumentSource(path, []byte(tt.src), root, ModeLight, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n != tt.wantCount {
				t.Fatalf("count = %d, want %d\n%s", n, tt.wantCount, out)
			}
			if pkg != tt.wantPkg {
				t.Fatalf("pkg = %q, want %q", pkg, tt.wantPkg)
			}
			if got := lineCount(string(out)); got != lineCount(tt.src) {
				t.Fatalf("line count changed: %d -> %d\n%s", lineCount(tt.src), got, out)
			}
			if got := callCount(string(out)); got != tt.wantCount {
				t.Fatalf("call count = %d, want %d\n%s", got, tt.wantCount, out)
			}
			for _, want := range tt.wantCalls {
				if !strings.Contains(string(out), want) {
					t.Errorf("missing %q in:\n%s", want, out)
				}
			}
			// The call must land immediately after the opening brace.
			for _, line := range strings.Split(string(out), "\n") {
				if i := strings.Index(line, "__cadr_trace("); i > 0 {
					if !strings.Contains(line[:i], "{") {
						t.Errorf("trace call not immediately after brace: %q", line)
					}
				}
			}
		})
	}
}

func TestInstrumentSourceFull(t *testing.T) {
	root := "/proj"
	path := "/proj/pkg/app.go"
	src := "package app\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n\nfunc Empty() {}\n"

	out, n, pkg, err := instrumentSource(path, []byte(src), root, ModeFull, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 || pkg != "app" {
		t.Fatalf("n=%d pkg=%q", n, pkg)
	}
	got := string(out)
	for _, want := range []string{
		`__cadr_s := __cadr_enter("Add","/proj/pkg/app.go",3);defer __cadr_exit(__cadr_s);`,
		`__cadr_s := __cadr_enter("Empty","/proj/pkg/app.go",7);defer __cadr_exit(__cadr_s);`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if callCount(got) != 0 {
		t.Errorf("full mode must not emit legacy __cadr_trace calls:\n%s", got)
	}
	if strings.Count(got, "__cadr_enter(") != 2 || strings.Count(got, "__cadr_exit(") != 2 {
		t.Errorf("expected 2 enter/exit pairs:\n%s", got)
	}
	if got := lineCount(string(out)); got != lineCount(src) {
		t.Fatalf("line count changed: %d -> %d\n%s", lineCount(src), got, out)
	}
}

func TestInstrumentSourceSharedImport(t *testing.T) {
	root := "/proj"
	path := "/proj/pkg/app.go"
	src := "package app\n\nimport \"fmt\"\n\nfunc Add(a, b int) int {\n\tfmt.Println(a)\n\treturn a + b\n}\n"
	const imp = "example.com/m/cadr_runtime"

	out, n, pkg, err := instrumentSource(path, []byte(src), root, ModeFull, imp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 || pkg != "app" {
		t.Fatalf("n=%d pkg=%q", n, pkg)
	}
	got := string(out)
	if !strings.Contains(got, "package app; import __cadr \""+imp+"\"") {
		t.Errorf("missing same-line shared import:\n%s", got)
	}
	if !strings.Contains(got, `__cadr_s := __cadr.Enter("Add","/proj/pkg/app.go",5);defer __cadr.Exit(__cadr_s);`) {
		t.Errorf("missing shared Enter/Exit call:\n%s", got)
	}
	if strings.Contains(got, "__cadr_enter(") || strings.Contains(got, "__cadr_trace(") {
		t.Errorf("shared mode must not use legacy calls:\n%s", got)
	}
	// Line count is unchanged, so declaration lines (and the static graph's
	// file:line identity) stay exact.
	if got := lineCount(string(out)); got != lineCount(src) {
		t.Fatalf("line count changed: %d -> %d\n%s", lineCount(src), got, out)
	}
}

func TestInstrumentSourceSharedImportSkips(t *testing.T) {
	root := "/proj"
	path := "/proj/pkg/app.go"
	// A file that already imports the runtime, and a cgo file, must be skipped.
	cases := [][]byte{
		[]byte("package app\n\nimport __cadr \"example.com/m/cadr_runtime\"\n\nfunc A() { _ = __cadr.Enter }\n"),
		[]byte("package app\n\n/*\n#include <stdio.h>\n*/\nimport \"C\"\n\nfunc A() {}\n"),
	}
	for _, src := range cases {
		out, n, _, err := instrumentSource(path, src, root, ModeFull, "example.com/m/cadr_runtime")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != 0 || out != nil {
			t.Fatalf("expected skip, got n=%d out=%q", n, out)
		}
	}
}

func TestInstrumentSourceSkips(t *testing.T) {
	src := []byte("package app\n\nfunc Keep() {}\n")
	cases := []struct {
		name string
		path string
		root string
		src  []byte
	}{
		{"outside root", "/other/app.go", "/proj", src},
		{"vendor", "/proj/vendor/lib/app.go", "/proj", src},
		{"cadr dir", "/proj/.cadr/x.go", "/proj", src},
		{"runtime file", "/proj/__cadr_runtime.go", "/proj", src},
		{
			"generated",
			"/proj/app.go",
			"/proj",
			[]byte("// Code generated by tool. DO NOT EDIT.\n\npackage app\n\nfunc Keep() {}\n"),
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			out, n, _, err := instrumentSource(tt.path, tt.src, tt.root, ModeLight, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n != 0 || out != nil {
				t.Fatalf("expected skip, got n=%d out=%q", n, out)
			}
		})
	}
}

func TestSplitArgs(t *testing.T) {
	args := []string{"-o", "/tmp/x.a", "-trimpath", "/tmp/b001=>", "-p", "main", "-importcfg", "/tmp/importcfg", "-pack", "/proj/main.go", "/proj/util.go"}
	flags, files := splitArgs(args)
	if len(files) != 2 || files[0] != "/proj/main.go" || files[1] != "/proj/util.go" {
		t.Fatalf("files = %v", files)
	}
	if len(flags) != 9 || flags[len(flags)-1] != "-pack" {
		t.Fatalf("flags = %v", flags)
	}

	flags, files = splitArgs([]string{"-V=full"})
	if len(files) != 0 || len(flags) != 1 {
		t.Fatalf("probe: flags=%v files=%v", flags, files)
	}
}

func TestPackageNamespace(t *testing.T) {
	a1 := PackageNamespace("/proj/pkg/a")
	a2 := PackageNamespace("/proj/pkg/a")
	b := PackageNamespace("/proj/pkg/b")
	if a1 != a2 {
		t.Fatalf("namespace not stable: %d vs %d", a1, a2)
	}
	if a1 == b {
		t.Fatalf("distinct dirs collided: %d", a1)
	}
}

func TestAddTrimpathMerges(t *testing.T) {
	got := addTrimpath([]string{"-importcfg", "/tmp/cfg", "-trimpath", "/tmp/b001=>"}, []string{"/tmp/a.go=>/proj/a.go"})
	if got[3] != "/tmp/b001=>;/tmp/a.go=>/proj/a.go" {
		t.Fatalf("merged trimpath = %q", got[3])
	}

	got = addTrimpath([]string{"-o", "/tmp/x.a"}, []string{"/tmp/a.go=>/proj/a.go"})
	if got[len(got)-2] != "-trimpath" || got[len(got)-1] != "/tmp/a.go=>/proj/a.go" {
		t.Fatalf("appended trimpath = %v", got)
	}

	got = addTrimpath([]string{"-trimpath=/tmp/b001=>"}, []string{"/tmp/a.go=>/proj/a.go"})
	if got[0] != "-trimpath=/tmp/b001=>;/tmp/a.go=>/proj/a.go" {
		t.Fatalf("equals-form trimpath = %q", got[0])
	}
}
