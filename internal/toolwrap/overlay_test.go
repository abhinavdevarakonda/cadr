package toolwrap

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func requireGo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
}

func TestOverlaySupportedPlainModule(t *testing.T) {
	requireGo(t)
	t.Setenv("GOWORK", "")
	t.Setenv("GOFLAGS", "")
	mod := t.TempDir()
	writeFile(t, filepath.Join(mod, "go.mod"), "module plainmod\n\ngo 1.26\n")

	ok, reason := OverlaySupported(mod, []string{"go", "run", "."})
	if !ok {
		t.Fatalf("plain module reported unsupported: %s", reason)
	}
}

func TestOverlaySupportedVendoring(t *testing.T) {
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")

	t.Run("vendor dir", func(t *testing.T) {
		mod := t.TempDir()
		if err := os.MkdirAll(filepath.Join(mod, "vendor"), 0755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(mod, "vendor", "modules.txt"), "module x\n")
		ok, reason := OverlaySupported(mod, nil)
		if ok || reason != "vendoring" {
			t.Fatalf("got ok=%v reason=%q, want vendoring", ok, reason)
		}
	})

	t.Run("mod flag", func(t *testing.T) {
		mod := t.TempDir()
		ok, reason := OverlaySupported(mod, []string{"go", "run", "-mod=vendor", "."})
		if ok || reason != "vendoring" {
			t.Fatalf("got ok=%v reason=%q, want vendoring", ok, reason)
		}
	})

	t.Run("goflags", func(t *testing.T) {
		t.Setenv("GOFLAGS", "-mod=vendor")
		mod := t.TempDir()
		ok, reason := OverlaySupported(mod, nil)
		if ok || reason != "vendoring" {
			t.Fatalf("got ok=%v reason=%q, want vendoring", ok, reason)
		}
	})
}

func TestOverlaySupportedWorkspace(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	t.Run("GOWORK env", func(t *testing.T) {
		mod := t.TempDir()
		t.Setenv("GOWORK", filepath.Join(mod, "go.work"))
		ok, reason := OverlaySupported(mod, nil)
		if ok || !strings.Contains(reason, "workspace") {
			t.Fatalf("got ok=%v reason=%q, want workspace", ok, reason)
		}
	})

	t.Run("go.work file", func(t *testing.T) {
		requireGo(t)
		t.Setenv("GOWORK", "")
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "go.work"), "go 1.26\n\nuse ./app\n")
		ok, reason := OverlaySupported(root, []string{"go", "run", "."})
		if ok || !strings.Contains(reason, "workspace") {
			t.Fatalf("got ok=%v reason=%q, want workspace", ok, reason)
		}
	})
}

// The overlay path must never write outside the cache dir: a read-only source
// tree (writable .cadr/cache) must still instrument. This guards the
// read-only-checkout promise.
func TestOverlayLeavesModuleUntouched(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod semantics differ")
	}
	requireGo(t)
	t.Setenv("GOWORK", "")
	t.Setenv("GOFLAGS", "")

	mod := t.TempDir()
	writeFile(t, filepath.Join(mod, "go.mod"), "module rotest\n\ngo 1.26\n")
	writeFile(t, filepath.Join(mod, "main.go"), "package main\n\nfunc A() { B() }\n\nfunc B() {}\n\nfunc main() { A() }\n")

	before := snapshotTree(t, mod)

	if err := os.Chmod(mod, 0555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(mod, 0755)

	cache := filepath.Join(t.TempDir(), "cache")
	ob, err := BuildOverlay(mod, cache)
	if err != nil {
		t.Fatalf("BuildOverlay on read-only module: %v", err)
	}
	if ob.Count == 0 {
		t.Fatal("no files instrumented")
	}

	after := snapshotTree(t, mod)
	if len(before) != len(after) {
		t.Fatalf("module tree changed: %d -> %d files", len(before), len(after))
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			t.Fatalf("module file disappeared: %s", path)
		}
	}
}

// snapshotTree maps every regular file under root (relative path -> size) so a
// test can prove nothing was added, removed or rewritten.
func snapshotTree(t *testing.T, root string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		rel, _ := filepath.Rel(root, path)
		out[rel] = info.Size()
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}
