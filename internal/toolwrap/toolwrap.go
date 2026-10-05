// Package toolwrap implements cadr's Go -toolexec wrapper.
//
// Go invokes the wrapper as:
//
//	cadr tool-wrap <abs-tool-path> [tool args...]
//
// Only the compile and link steps are touched: compile is rewritten to splice
// __cadr_trace calls into user packages and to add the self-contained runtime
// file, and both compile and link import configs are augmented with the
// standard-library archives the runtime depends on (cmd/go only lists the
// packages the user's own source imports, so injected imports would otherwise
// fail to resolve).
package toolwrap

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Run executes the wrapped tool and returns its exit code.
func Run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "cadr tool-wrap: missing tool argument")
		return 2
	}
	tool, rest := args[0], args[1:]
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(tool), ".exe"))

	if base == "compile" {
		if newArgs, cleanup, ok := maybeInstrumentCompile(rest); ok {
			defer cleanup()
			return execTool(tool, newArgs)
		}
	}
	if base == "link" {
		if newArgs, cleanup, ok := maybeAugmentLink(rest); ok {
			defer cleanup()
			return execTool(tool, newArgs)
		}
	}
	return execTool(tool, rest)
}

// maybeInstrumentCompile rewrites a compile invocation. It returns ok=false
// when there is nothing to do, in which case the caller must run the tool
// unchanged.
func maybeInstrumentCompile(rest []string) (newArgs []string, cleanup func(), ok bool) {
	root := os.Getenv("CADR_PROJECT_ROOT")
	frag := os.Getenv("CADR_IMPORTCFG")
	if root == "" || frag == "" {
		return nil, nil, false
	}
	flags, files := splitArgs(rest)
	if len(files) == 0 {
		return nil, nil, false
	}

	type prepared struct {
		orig string
		out  []byte
	}
	var todo []prepared
	pkg := ""
	total := 0
	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			continue
		}
		src, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		out, n, p, err := instrumentSource(abs, src, root)
		if err != nil {
			// Leave unparsable files untouched; the real compiler reports them.
			continue
		}
		if n == 0 {
			continue
		}
		if pkg == "" {
			pkg = p
		}
		total += n
		todo = append(todo, prepared{orig: f, out: out})
	}
	if total == 0 || pkg == "" {
		return nil, nil, false
	}

	tmpDir, err := os.MkdirTemp("", "cadr-instr-")
	if err != nil {
		return nil, nil, false
	}
	cleanup = func() { _ = os.RemoveAll(tmpDir) }

	mapped := make(map[string]string, len(todo))
	var rewrites []string
	for i, p := range todo {
		name := fmt.Sprintf("%03d_%s", i, filepath.Base(p.orig))
		tmpPath := filepath.Join(tmpDir, name)
		if err := os.WriteFile(tmpPath, p.out, 0644); err != nil {
			cleanup()
			return nil, nil, false
		}
		mapped[p.orig] = tmpPath
		rewrites = append(rewrites, tmpPath+"=>"+p.orig)
	}

	runtimePath := filepath.Join(tmpDir, "__cadr_runtime.go")
	if err := os.WriteFile(runtimePath, []byte(runtimeSource(pkg)), 0644); err != nil {
		cleanup()
		return nil, nil, false
	}

	flags, ok = augmentImportcfg(flags, frag, tmpDir)
	if !ok {
		cleanup()
		return nil, nil, false
	}

	newFiles := make([]string, 0, len(files)+1)
	for _, f := range files {
		if tmpPath, hit := mapped[f]; hit {
			newFiles = append(newFiles, tmpPath)
		} else {
			newFiles = append(newFiles, f)
		}
	}
	newFiles = append(newFiles, runtimePath)

	// Strip the temp dir from any recorded path, then map each copy back to the
	// original source for panics/DWARF.
	rewrites = append(rewrites, tmpDir+"=>")
	flags = addTrimpath(flags, rewrites)

	newArgs = append(append([]string{}, flags...), newFiles...)
	return newArgs, cleanup, true
}

// maybeAugmentLink appends the runtime's import fragment to the link import
// config so the injected stdlib dependencies resolve.
func maybeAugmentLink(rest []string) (newArgs []string, cleanup func(), ok bool) {
	frag := os.Getenv("CADR_IMPORTCFG")
	if frag == "" {
		return nil, nil, false
	}
	fragData, err := os.ReadFile(frag)
	if err != nil || len(fragData) == 0 {
		return nil, nil, false
	}
	fragData = append(fragData, '\n')

	for i, a := range rest {
		var cfgPath string
		switch {
		case a == "-importcfg" && i+1 < len(rest):
			cfgPath = rest[i+1]
		case strings.HasPrefix(a, "-importcfg="):
			cfgPath = strings.TrimPrefix(a, "-importcfg=")
		default:
			continue
		}
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			return nil, nil, false
		}
		tmpDir, err := os.MkdirTemp("", "cadr-linkcfg-")
		if err != nil {
			return nil, nil, false
		}
		p := filepath.Join(tmpDir, "importcfg.link")
		out := append(append([]byte{}, data...), '\n')
		out = append(out, fragData...)
		if err := os.WriteFile(p, out, 0644); err != nil {
			_ = os.RemoveAll(tmpDir)
			return nil, nil, false
		}
		newRest := append([]string{}, rest...)
		if a == "-importcfg" {
			newRest[i+1] = p
		} else {
			newRest[i] = "-importcfg=" + p
		}
		return newRest, func() { _ = os.RemoveAll(tmpDir) }, true
	}
	return nil, nil, false
}

// augmentImportcfg copies the compile import config and appends the fragment,
// returning updated flags. ok=false means the flag was absent or unreadable.
func augmentImportcfg(flags []string, frag, tmpDir string) ([]string, bool) {
	fragData, err := os.ReadFile(frag)
	if err != nil || len(fragData) == 0 {
		return nil, false
	}
	fragData = append(fragData, '\n')

	for i, a := range flags {
		var cfgPath string
		switch {
		case a == "-importcfg" && i+1 < len(flags):
			cfgPath = flags[i+1]
		case strings.HasPrefix(a, "-importcfg="):
			cfgPath = strings.TrimPrefix(a, "-importcfg=")
		default:
			continue
		}
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			return nil, false
		}
		p := filepath.Join(tmpDir, "importcfg")
		out := append(append([]byte{}, data...), '\n')
		out = append(out, fragData...)
		if err := os.WriteFile(p, out, 0644); err != nil {
			return nil, false
		}
		if a == "-importcfg" {
			flags[i+1] = p
		} else {
			flags[i] = "-importcfg=" + p
		}
		return flags, true
	}
	return nil, false
}

// addTrimpath merges rewrite rules into an existing -trimpath value.
func addTrimpath(flags, rules []string) []string {
	val := strings.Join(rules, ";")
	for i, a := range flags {
		if a == "-trimpath" && i+1 < len(flags) {
			if flags[i+1] == "" {
				flags[i+1] = val
			} else {
				flags[i+1] = flags[i+1] + ";" + val
			}
			return flags
		}
		if strings.HasPrefix(a, "-trimpath=") {
			existing := strings.TrimPrefix(a, "-trimpath=")
			if existing == "" {
				flags[i] = "-trimpath=" + val
			} else {
				flags[i] = "-trimpath=" + existing + ";" + val
			}
			return flags
		}
	}
	return append(flags, "-trimpath", val)
}

func execTool(tool string, args []string) int {
	cmd := exec.Command(tool, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "cadr tool-wrap: %v\n", err)
		return 1
	}
	return 0
}
