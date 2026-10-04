package diff

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestComputeDiffAndFunctionHasDiff(t *testing.T) {
	oldText := "package main\n\nfunc Clean() {\n\tprintln(\"clean\")\n}\n\nfunc Modified() {\n\tprintln(\"old\")\n}\n"
	newText := "package main\n\nfunc Clean() {\n\tprintln(\"clean\")\n}\n\nfunc Modified() {\n\tprintln(\"new\")\n\tprintln(\"extra\")\n}\n"

	fd := ComputeDiff(oldText, newText, "main.go", "/path/main.go", time.Now())
	if fd.AddedCount != 2 {
		t.Errorf("expected 2 added lines, got %d", fd.AddedCount)
	}
	if fd.DeletedCount != 1 {
		t.Errorf("expected 1 deleted line, got %d", fd.DeletedCount)
	}

	// Clean function is lines 3..5 in newText
	if fd.FunctionHasDiff(3, 5) {
		t.Errorf("expected Clean() not to have diff")
	}

	// Modified function is lines 7..10 in newText
	if !fd.FunctionHasDiff(7, 10) {
		t.Errorf("expected Modified() to have diff")
	}
}

func TestFormatRelativeTime(t *testing.T) {
	now := time.Now()
	if got := FormatRelativeTime(now.Add(-5 * time.Second)); got != "just now" {
		t.Errorf("expected 'just now', got %q", got)
	}
	if got := FormatRelativeTime(now.Add(-45 * time.Second)); got != "45s ago" {
		t.Errorf("expected '45s ago', got %q", got)
	}
	if got := FormatRelativeTime(now.Add(-5 * time.Minute)); got != "5m ago" {
		t.Errorf("expected '5m ago', got %q", got)
	}
	if got := FormatRelativeTime(now.Add(-2 * time.Hour)); got != "2h ago" {
		t.Errorf("expected '2h ago', got %q", got)
	}
	if got := FormatRelativeTime(now.Add(-3 * 24 * time.Hour)); got != "3 days ago" {
		t.Errorf("expected '3 days ago', got %q", got)
	}
}

func TestManagerAcknowledge(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cadr-diff-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	fileRel := "foo.go"
	fullPath := filepath.Join(tmpDir, fileRel)
	if err := os.WriteFile(fullPath, []byte("package foo\n\nfunc Bar() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(tmpDir)
	// Acknowledge this initial version as baseline
	if err := mgr.AcknowledgeFile(fileRel); err != nil {
		t.Fatal(err)
	}

	// Scan: should be no diff
	fd := mgr.ScanFile(fileRel)
	if fd != nil {
		t.Fatalf("expected nil fd after initial acknowledge, got %+v", fd)
	}

	// Modify file
	if err := os.WriteFile(fullPath, []byte("package foo\n\nfunc Bar() {\n\tprintln(\"hi\")\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Scan: should detect changes
	fd = mgr.ScanFile(fileRel)
	if fd == nil {
		t.Fatal("expected diff after edit, got nil")
	}
	if fd.AddedCount != 3 || fd.DeletedCount != 1 {
		t.Fatalf("expected 3 added lines and 1 deleted line, got added=%d, deleted=%d", fd.AddedCount, fd.DeletedCount)
	}

	// Acknowledge all
	if err := mgr.AcknowledgeAll(); err != nil {
		t.Fatal(err)
	}

	// Scan again: should be nil
	fd = mgr.ScanFile(fileRel)
	if fd != nil {
		t.Fatalf("expected nil diff after AcknowledgeAll, got %+v", fd)
	}
}

func TestAcknowledgeFunction(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cadr-fn-diff-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	fileRel := "service.go"
	fullPath := filepath.Join(tmpDir, fileRel)
	original := "package main\n\nfunc One() {\n\tprintln(\"1\")\n}\n\nfunc Two() {\n\tprintln(\"2\")\n}\n"
	if err := os.WriteFile(fullPath, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(tmpDir)
	if err := mgr.AcknowledgeFile(fileRel); err != nil {
		t.Fatal(err)
	}

	// Modify both One() and Two()
	modified := "package main\n\nfunc One() {\n\tprintln(\"1-mod\")\n}\n\nfunc Two() {\n\tprintln(\"2-mod\")\n}\n"
	if err := os.WriteFile(fullPath, []byte(modified), 0644); err != nil {
		t.Fatal(err)
	}

	fd := mgr.ScanFile(fileRel)
	if fd == nil {
		t.Fatal("expected diff, got nil")
	}

	// In modified: One() is lines 3..5, Two() is lines 7..9
	if !fd.FunctionHasDiff(3, 5) {
		t.Errorf("expected One() to have diff")
	}
	if !fd.FunctionHasDiff(7, 9) {
		t.Errorf("expected Two() to have diff")
	}

	// Acknowledge ONLY One() (lines 3..5)
	if err := mgr.AcknowledgeFunction(fileRel, 3, 5); err != nil {
		t.Fatal(err)
	}

	fdAfter := mgr.ScanFile(fileRel)
	if fdAfter == nil {
		t.Fatal("expected file to still have diffs for Two(), but got nil")
	}
	if fdAfter.FunctionHasDiff(3, 5) {
		t.Errorf("expected One() diff to be cleared after acknowledge")
	}
	if !fdAfter.FunctionHasDiff(7, 9) {
		t.Errorf("expected Two() diff to still be present")
	}
}

func TestScanGitStatus(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cadr-git-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Init git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Skip("git not available")
	}
	_ = exec.Command("git", "-C", tmpDir, "config", "user.email", "test@test.com").Run()
	_ = exec.Command("git", "-C", tmpDir, "config", "user.name", "Test").Run()

	filePath := filepath.Join(tmpDir, "hello.go")
	if err := os.WriteFile(filePath, []byte("package main\n\nfunc Hello() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_ = exec.Command("git", "-C", tmpDir, "add", "hello.go").Run()
	_ = exec.Command("git", "-C", tmpDir, "commit", "-m", "initial").Run()

	mgr := NewManager(tmpDir)
	diffs, err := mgr.ScanAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 0 {
		t.Fatalf("expected 0 diffs right after commit, got %d", len(diffs))
	}

	// Modify file
	if err := os.WriteFile(filePath, []byte("package main\n\nfunc Hello() {\n\tprintln(\"world\")\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	diffs, err = mgr.ScanAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff after edit, got %d", len(diffs))
	}
	if diffs["hello.go"] == nil {
		t.Fatal("expected hello.go in diffs map")
	}
	if !diffs["hello.go"].FunctionHasDiff(3, 5) {
		t.Errorf("expected Hello() to have diff")
	}

	// Test index.lock resilience: if .git/index.lock exists, ScanAll should preserve existing diffs
	gitLock := filepath.Join(tmpDir, ".git", "index.lock")
	if err := os.WriteFile(gitLock, []byte("lock"), 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(gitLock)

	lockedDiffs, err := mgr.ScanAll()
	if err != nil {
		t.Fatalf("expected no error during index.lock, got: %v", err)
	}
	if len(lockedDiffs) != 1 || lockedDiffs["hello.go"] == nil {
		t.Fatalf("expected diffs to be preserved when index.lock is active, got: %v", lockedDiffs)
	}
}

func TestScanFileIgnoresDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir)
	subDir := filepath.Join(tmpDir, "some_dir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if fd := mgr.ScanFile("some_dir"); fd != nil {
		t.Errorf("expected nil FileDiff for directory, got: %v", fd)
	}
}

func TestWatcherGitignoreAndEphemeral(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .gitignore ignoring "raw/" and "*.log"
	gitIgnorePath := filepath.Join(tmpDir, ".gitignore")
	if err := os.WriteFile(gitIgnorePath, []byte("raw/\n*.log\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create raw directory
	rawDir := filepath.Join(tmpDir, "raw")
	if err := os.MkdirAll(rawDir, 0755); err != nil {
		t.Fatal(err)
	}

	var triggered int
	watcher, err := StartWatcher(tmpDir, func() {
		triggered++
	})
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()

	// Verify ignored path
	if !watcher.isIgnored(filepath.Join(tmpDir, "raw", "data.json")) {
		t.Errorf("expected raw/data.json to be ignored")
	}
	if !watcher.isIgnored(filepath.Join(tmpDir, "app.log")) {
		t.Errorf("expected app.log to be ignored")
	}
	if watcher.isIgnored(filepath.Join(tmpDir, "main.go")) {
		t.Errorf("expected main.go not to be ignored")
	}

	// Verify ephemeral files
	if !isEphemeralFile(".main.go.swp") {
		t.Errorf("expected .swp to be ephemeral")
	}
	if !isEphemeralFile("file.go.tmp") {
		t.Errorf("expected .tmp to be ephemeral")
	}
	if !isEphemeralFile("file.go~") {
		t.Errorf("expected ~ to be ephemeral")
	}
	if isEphemeralFile("main.go") {
		t.Errorf("expected main.go not to be ephemeral")
	}
}

func TestPruneCachedBaselineOnCommit(t *testing.T) {
	tmpDir := t.TempDir()

	// Init git repo in tmpDir
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Skip("git not available")
	}
	_ = exec.Command("git", "-C", tmpDir, "config", "user.email", "test@test.com").Run()
	_ = exec.Command("git", "-C", tmpDir, "config", "user.name", "Test").Run()

	filePath := filepath.Join(tmpDir, "file.go")
	if err := os.WriteFile(filePath, []byte("package main\n\nfunc V1() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_ = exec.Command("git", "-C", tmpDir, "add", "file.go").Run()
	_ = exec.Command("git", "-C", tmpDir, "commit", "-m", "v1").Run()

	mgr := NewManager(tmpDir)

	// Modify file
	if err := os.WriteFile(filePath, []byte("package main\n\nfunc V1() {}\nfunc V2() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Verify diff exists before acknowledge
	if fd := mgr.ScanFile("file.go"); fd == nil {
		t.Fatal("expected diff before acknowledge, got nil")
	}

	// Acknowledge the file
	if err := mgr.AcknowledgeFile("file.go"); err != nil {
		t.Fatal(err)
	}

	// Baseline should exist on disk while uncommitted
	baselineFile := mgr.BaselinePath("file.go")
	if _, err := os.Stat(baselineFile); err != nil {
		t.Fatalf("expected baseline file to exist in cache, got err: %v", err)
	}

	// Scanning should show no diff now
	if fd := mgr.ScanFile("file.go"); fd != nil {
		t.Fatalf("expected nil diff after acknowledge, got: %+v", fd)
	}

	// Now commit the file in git
	_ = exec.Command("git", "-C", tmpDir, "add", "file.go").Run()
	_ = exec.Command("git", "-C", tmpDir, "commit", "-m", "v2").Run()

	// Scan again: should prune the baseline from cache since it matches git HEAD
	if fd := mgr.ScanFile("file.go"); fd != nil {
		t.Fatalf("expected nil diff after commit, got: %+v", fd)
	}

	// Verify baseline file was pruned
	if _, err := os.Stat(baselineFile); !os.IsNotExist(err) {
		t.Errorf("expected baseline file to be pruned after commit, but it still exists")
	}
}
