package toolwrap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// goRuntimeDeps are the standard-library packages the injected runtime imports.
// cmd/go only lists a package's own direct imports in the compile import
// config, so these must be added explicitly by the wrapper.
var goRuntimeDeps = []string{"net", "os", "runtime", "sync", "sync/atomic", "time"}

// PrepareOverlay instruments the module into an overlay and prepares the shared
// runtime module + alternate modfile. ok=false means the caller should fall back
// to the legacy -toolexec path (or run uninstrumented).
func PrepareOverlay(root string) (*OverlayBuild, bool) {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	cacheBase := filepath.Join(abs, ".cadr", "cache")
	if err := os.MkdirAll(cacheBase, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "cadr: go tracing disabled: %v\n", err)
		return nil, false
	}
	ob, err := BuildOverlay(abs, cacheBase)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadr: go tracing disabled: %v\n", err)
		return nil, false
	}
	return ob, true
}

// PrepareEnv builds the environment for an instrumented go invocation: an
// isolated build cache and a pre-resolved import fragment for the injected
// runtime's standard-library dependencies. ok=false means the go command should
// run uninstrumented.
func PrepareEnv(root string) (env []string, ok bool) {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return prepareEnv(abs, filepath.Join(abs, ".cadr", "cache"))
}

func prepareEnv(root, cacheBase string) (env []string, ok bool) {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	if cacheAbs, err := filepath.Abs(cacheBase); err == nil {
		cacheBase = cacheAbs
	}
	key := CacheKey(TraceModeFromEnv())

	cacheDir := filepath.Join(cacheBase, "go-build", key)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "cadr: go tracing disabled: %v\n", err)
		return nil, false
	}

	env = append(os.Environ(),
		"GOCACHE="+cacheDir,
		"CADR_PROJECT_ROOT="+abs,
	)

	fragPath := filepath.Join(cacheBase, "importcfg-"+key+".cfg")
	if err := buildImportFragment(abs, env, fragPath); err != nil {
		fmt.Fprintf(os.Stderr, "cadr: go tracing disabled: %v\n", err)
		return nil, false
	}
	env = append(env, "CADR_IMPORTCFG="+fragPath)
	return env, true
}

// buildImportFragment exports the runtime's dependency closure and records
// every archive as a `packagefile` line of a compile import config.
func buildImportFragment(dir string, env []string, fragPath string) error {
	args := append([]string{"list", "-export", "-deps", "-json"}, goRuntimeDeps...)
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("resolving go runtime imports: %s", msg)
	}

	dec := json.NewDecoder(bytes.NewReader(out))
	var b strings.Builder
	for {
		var p struct {
			ImportPath string
			Export     string
		}
		if err := dec.Decode(&p); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("parsing go list output: %w", err)
		}
		if p.ImportPath == "" || p.Export == "" || p.ImportPath == "unsafe" {
			continue
		}
		fmt.Fprintf(&b, "packagefile %s=%s\n", p.ImportPath, p.Export)
	}
	if b.Len() == 0 {
		return fmt.Errorf("go list produced no importable packages")
	}
	return os.WriteFile(fragPath, []byte(b.String()), 0644)
}

// ToolexecValue returns the value for go's -toolexec flag that routes tool
// invocations back through this binary.
func ToolexecValue(exe string) string {
	return quoteArg(exe) + " tool-wrap"
}

// quoteArg mirrors cmd/internal/quoted.Join for a single argument: quote only
// when the value contains whitespace or quotes. cmd/go parses -toolexec with
// quoted.Split, which performs no unescaping, so literal paths (including
// Windows backslashes) are preserved.
func quoteArg(s string) string {
	if !strings.ContainsAny(s, " \t\n\r\"'") {
		return s
	}
	if !strings.Contains(s, `"`) {
		return `"` + s + `"`
	}
	if !strings.Contains(s, `'`) {
		return `'` + s + `'`
	}
	return s
}
