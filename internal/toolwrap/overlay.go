package toolwrap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runtimeModulePath is the import path of the generated shared runtime module.
// It is deliberately not under the user's module so `go test ./...` never
// matches it.
const runtimeModulePath = "cadr.internal/runtime"

// moduleInfo is the subset of `go list -m -json` we need.
type moduleInfo struct {
	Path string
	Dir  string
}

// ModuleInfo resolves the main module for root.
func ModuleInfo(root string) (moduleInfo, bool) {
	cmd := exec.Command("go", "list", "-m", "-json")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return moduleInfo{}, false
	}
	var m moduleInfo
	if json.Unmarshal(out, &m) != nil || m.Path == "" || m.Dir == "" {
		return moduleInfo{}, false
	}
	return m, true
}

// overlayKeys returns every path form the go tool might use to look up a file:
// the path as walked, its symlink-resolved form, and the /private-stripped or
// /private-prefixed form (macOS temp dirs). Without this, an overlay under
// /var/folders is silently ignored because the go tool resolved it to
// /private/var/folders.
func overlayKeys(path string) []string {
	keys := []string{path}
	if real, err := filepath.EvalSymlinks(path); err == nil && real != path {
		keys = append(keys, real)
	}
	var alt string
	if strings.HasPrefix(path, "/private/") {
		alt = strings.TrimPrefix(path, "/private")
	} else {
		alt = "/private" + path
	}
	if _, err := os.Stat(alt); err == nil {
		keys = append(keys, alt)
	}
	return keys
}

// overlayConfig matches cmd/go's -overlay JSON.
type overlayConfig struct {
	Replace map[string]string `json:"Replace"`
}

// OverlayBuild is a prepared overlay build: the environment to run the go
// command with, plus the -overlay and -modfile paths to inject.
type OverlayBuild struct {
	Env         []string
	OverlayPath string
	ModFile     string
	Count       int
}

// BuildOverlay instruments every project Go file into cacheDir and prepares a
// shared runtime module plus an alternate modfile that requires it. The go tool
// then compiles a single process-wide tracing runtime, so span/seq are globally
// unique and cross-package event order is exact.
//
// Nothing is written into the user's module: the runtime module, the alternate
// modfile and the instrumented copies all live under cacheDir (`.cadr/cache`).
func BuildOverlay(root, cacheDir string) (*OverlayBuild, error) {
	mod, ok := ModuleInfo(root)
	if !ok {
		return nil, fmt.Errorf("not inside a Go module")
	}
	// The go tool canonicalises symlinked temp dirs (/var -> /private/var on
	// macOS), so overlay keys must use the resolved paths or they are ignored.
	modDir := mod.Dir
	goModPath := filepath.Join(mod.Dir, "go.mod")
	goModData, err := os.ReadFile(goModPath)
	if err != nil {
		return nil, fmt.Errorf("reading go.mod: %w", err)
	}

	runtimeDir := filepath.Join(cacheDir, "runtime")
	if err := os.MkdirAll(runtimeDir, 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "go.mod"), []byte("module "+runtimeModulePath+"\n\ngo 1.26\n"), 0644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "runtime.go"), []byte(sharedRuntimeSource()), 0644); err != nil {
		return nil, err
	}

	// Alternate modfile: the user's go.mod plus the local runtime requirement.
	modFile := filepath.Join(cacheDir, "cadr.mod")
	modCopy := strings.TrimRight(string(goModData), "\n") + "\n\nrequire " + runtimeModulePath + " v0.0.0\n\nreplace " + runtimeModulePath + " => " + runtimeDir + "\n"
	if err := os.WriteFile(modFile, []byte(modCopy), 0644); err != nil {
		return nil, err
	}
	// -modfile uses a sibling .sum; copy the user's go.sum so dependency
	// checksums still verify. Local replaces need no entry.
	if sumData, serr := os.ReadFile(filepath.Join(mod.Dir, "go.sum")); serr == nil {
		if werr := os.WriteFile(filepath.Join(cacheDir, "cadr.sum"), sumData, 0644); werr != nil {
			return nil, werr
		}
	}

	instrDir := filepath.Join(cacheDir, "instr")
	if err := os.MkdirAll(instrDir, 0755); err != nil {
		return nil, err
	}
	mode := TraceModeFromEnv()
	repl := map[string]string{}
	count := 0

	walkErr := filepath.WalkDir(modDir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if path != modDir {
				if base == ".cadr" || base == "vendor" || base == "node_modules" || base == "testdata" ||
					strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_") {
					return filepath.SkipDir
				}
				// Skip nested modules; they are not part of this build.
				if _, sterr := os.Stat(filepath.Join(path, "go.mod")); sterr == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		out, n, _, ierr := instrumentSource(path, src, modDir, mode, runtimeModulePath)
		if ierr != nil || n == 0 {
			return nil
		}
		sum := sha256.Sum256([]byte(instrumentVersion + "|" + mode + "|" + path + "|" + string(src)))
		outPath := filepath.Join(instrDir, "f_"+hex.EncodeToString(sum[:])[:20]+".go")
		if werr := os.WriteFile(outPath, out, 0644); werr != nil {
			return werr
		}
		for _, k := range overlayKeys(path) {
			repl[k] = outPath
		}
		count++
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	overlayPath := filepath.Join(instrDir, "overlay.json")
	data, err := json.Marshal(overlayConfig{Replace: repl})
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(overlayPath, data, 0644); err != nil {
		return nil, err
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return &OverlayBuild{
		Env:         append(os.Environ(), "CADR_PROJECT_ROOT="+abs),
		OverlayPath: overlayPath,
		ModFile:     modFile,
		Count:       count,
	}, nil
}
