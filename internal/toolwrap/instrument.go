package toolwrap

import (
	"bufio"
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// generatedRe matches the canonical "generated file" marker described by
// https://go.dev/s/generatedcode.
var generatedRe = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// insertion is a single __cadr_trace call to splice into a source file.
type insertion struct {
	offset int
	text   string
}

// splitArgs separates a `compile` invocation's flag arguments from its
// trailing source-file list. cmd/go appends the absolute .go files last
// (GOROOT/src/cmd/go/internal/work/gc.go), so a suffix scan is exact.
func splitArgs(args []string) (flags, files []string) {
	i := len(args)
	for i > 0 && strings.HasSuffix(args[i-1], ".go") {
		i--
	}
	return args[:i], args[i:]
}

// relWithin returns path relative to root, resolving symlinks when the direct
// comparison fails. macOS reports temp dirs as /var/... while the go tool
// canonicalises them to /private/var/..., so both forms must be accepted.
func relWithin(path, root string) (string, bool) {
	check := func(p, r string) (string, bool) {
		rel, err := filepath.Rel(r, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", false
		}
		return rel, true
	}
	if rel, ok := check(path, root); ok {
		return rel, true
	}
	if rp, err := filepath.EvalSymlinks(path); err == nil {
		if rr, err := filepath.EvalSymlinks(root); err == nil {
			if rel, ok := check(rp, rr); ok {
				return rel, true
			}
		}
	}
	return "", false
}

// shouldInstrument reports whether a source file belongs to the user's project
// and is safe to rewrite.
func shouldInstrument(path, root string) bool {
	if !strings.HasSuffix(path, ".go") {
		return false
	}
	if filepath.Base(path) == "__cadr_runtime.go" {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	if root == "" {
		return false
	}
	rel, ok := relWithin(abs, root)
	if !ok {
		return false
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		switch part {
		case "vendor", ".cadr", "node_modules", "testdata":
			return false
		}
	}
	return true
}

// isGenerated reports whether src carries a "Code generated ... DO NOT EDIT."
// marker before its package clause.
func isGenerated(src []byte) bool {
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "package ") {
			return false
		}
		if generatedRe.MatchString(line) {
			return true
		}
	}
	return false
}

// receiverTypeName extracts the base type name from a method receiver
// expression, unwrapping pointers, parentheses and generic instantiations.
func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.ParenExpr:
		return receiverTypeName(t.X)
	case *ast.IndexExpr:
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	}
	return ""
}

// planInsertions returns the trace calls to splice into f. Function-level
// granularity only: name (Recv.Method for methods), absolute file and the
// declaration line, matching the graph's Symbol.Name/Path/StartLine.
//
// mode full injects an enter call plus a deferred exit (call tree, durations);
// mode light keeps the legacy single entry call. When runtimeImport is non-empty
// the calls go through the shared `__cadr` package (overlay build); otherwise
// they use the injected per-package `__cadr_*` runtime (legacy -toolexec).
func planInsertions(fset *token.FileSet, f *ast.File, absPath, mode, runtimeImport string) []insertion {
	var out []insertion
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil || fd.Name == nil {
			continue
		}
		name := fd.Name.Name
		if name == "init" || name == "_" {
			continue
		}
		full := name
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			if rt := receiverTypeName(fd.Recv.List[0].Type); rt != "" {
				full = rt + "." + name
			}
		}
		line := fset.Position(fd.Pos()).Line
		offset := fset.Position(fd.Body.Lbrace).Offset + 1
		// No trailing newline: line numbers of all following code stay exact.
		var text string
		switch {
		case runtimeImport != "" && mode == ModeLight:
			text = " __cadr.Trace(" + strconv.Quote(full) + "," + strconv.Quote(absPath) + "," + strconv.Itoa(line) + ");"
		case runtimeImport != "":
			prelude := ""
			if f.Name.Name == "main" && name == "main" && fd.Recv == nil {
				prelude = " defer __cadr.Flush();"
			}
			text = prelude + " __cadr_s := __cadr.Enter(" + strconv.Quote(full) + "," + strconv.Quote(absPath) + "," + strconv.Itoa(line) + ");defer __cadr.Exit(__cadr_s);"
		case mode == ModeLight:
			text = " __cadr_trace(" + strconv.Quote(full) + "," + strconv.Quote(absPath) + "," + strconv.Itoa(line) + ");"
		default:
			// The instrumented main registers the drain barrier first so it runs
			// last (defer LIFO) and flushes the root exit before process exit.
			prelude := ""
			if f.Name.Name == "main" && name == "main" && fd.Recv == nil {
				prelude = " defer __cadr_flush();"
			}
			text = prelude + " __cadr_s := __cadr_enter(" + strconv.Quote(full) + "," + strconv.Quote(absPath) + "," + strconv.Itoa(line) + ");defer __cadr_exit(__cadr_s);"
		}
		out = append(out, insertion{offset: offset, text: text})
	}
	return out
}

// importsPath reports whether f imports importPath.
func importsPath(f *ast.File, importPath string) bool {
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, "\"") == importPath {
			return true
		}
	}
	return false
}

// importsC reports whether f is a cgo file (import "C").
func importsC(f *ast.File) bool {
	return importsPath(f, "C")
}

// applyInsertions splices ins into src back-to-front so earlier offsets remain
// valid.
func applyInsertions(src []byte, ins []insertion) []byte {
	sort.Slice(ins, func(i, j int) bool { return ins[i].offset > ins[j].offset })
	out := src
	for _, in := range ins {
		if in.offset < 0 || in.offset > len(out) {
			continue
		}
		next := make([]byte, 0, len(out)+len(in.text))
		next = append(next, out[:in.offset]...)
		next = append(next, in.text...)
		next = append(next, out[in.offset:]...)
		out = next
	}
	return out
}

// instrumentSource rewrites one file, returning the instrumented source, the
// number of trace calls inserted and the file's package name. A nil out with
// nil error means the file was intentionally left untouched (outside root,
// generated, cgo, already instrumented, or no instrumentable functions). mode
// is ModeFull or ModeLight. runtimeImport selects the shared overlay runtime
// package; empty means the legacy per-package runtime.
func instrumentSource(path string, src []byte, root, mode, runtimeImport string) (out []byte, count int, pkg string, err error) {
	if !shouldInstrument(path, root) || isGenerated(src) {
		return nil, 0, "", nil
	}
	abs, absErr := filepath.Abs(path)
	if absErr != nil {
		abs = path
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, abs, src, parser.ParseComments)
	if err != nil {
		return nil, 0, "", err
	}
	if runtimeImport != "" {
		// The overlay build adds the import itself, so a file that already
		// imports the runtime (or is cgo) is left alone.
		if importsPath(f, runtimeImport) || importsC(f) {
			return nil, 0, f.Name.Name, nil
		}
	}
	ins := planInsertions(fset, f, abs, mode, runtimeImport)
	if len(ins) == 0 {
		return nil, 0, f.Name.Name, nil
	}
	if runtimeImport != "" {
		// `package X; import __cadr "..."` on the package line keeps every
		// following line number exact.
		ins = append(ins, insertion{
			offset: fset.Position(f.Name.End()).Offset,
			text:   "; import __cadr " + strconv.Quote(runtimeImport),
		})
	}
	return applyInsertions(src, ins), len(ins), f.Name.Name, nil
}
