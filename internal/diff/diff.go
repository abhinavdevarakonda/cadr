package diff

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sergi/go-diff/diffmatchpatch"
)

type Op int

const (
	OpEqual Op = iota
	OpInsert
	OpDelete
)

type Line struct {
	Op              Op
	Text            string
	OldLine         int // 1-based line in baseline (0 if inserted)
	NewLine         int // 1-based line in current file (0 if deleted)
	DeletedNearLine int // 1-based line in current file where deletion occurred
}

type Hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []Line
}

type FileDiff struct {
	RelPath        string
	FullPath       string
	ModTime        time.Time
	AddedCount     int
	DeletedCount   int
	Lines          []Line
	Hunks          []Hunk
	ChangedLineSet map[int]bool // 1-based line numbers in current file that changed
}

// FunctionHasDiff returns true if any diff occurred within the function's [startLine, endLine] range.
func (fd *FileDiff) FunctionHasDiff(startLine, endLine int) bool {
	if fd == nil {
		return false
	}
	for line := range fd.ChangedLineSet {
		if line >= startLine && line <= endLine {
			return true
		}
	}
	return false
}

// ComputeDiff calculates line-by-line diff between oldContent and newContent using diffmatchpatch.
func ComputeDiff(oldContent, newContent string, relPath, fullPath string, modTime time.Time) *FileDiff {
	dmp := diffmatchpatch.New()
	a, b, lineArray := dmp.DiffLinesToChars(oldContent, newContent)
	diffs := dmp.DiffMain(a, b, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArray)

	fd := &FileDiff{
		RelPath:        relPath,
		FullPath:       fullPath,
		ModTime:        modTime,
		ChangedLineSet: make(map[int]bool),
	}

	oldLine := 1
	newLine := 1
	var allLines []Line

	for _, d := range diffs {
		lines := splitLinesPreserve(d.Text)
		for _, text := range lines {
			switch d.Type {
			case diffmatchpatch.DiffEqual:
				allLines = append(allLines, Line{
					Op:      OpEqual,
					Text:    text,
					OldLine: oldLine,
					NewLine: newLine,
				})
				oldLine++
				newLine++
			case diffmatchpatch.DiffInsert:
				allLines = append(allLines, Line{
					Op:      OpInsert,
					Text:    text,
					OldLine: 0,
					NewLine: newLine,
				})
				fd.AddedCount++
				fd.ChangedLineSet[newLine] = true
				newLine++
			case diffmatchpatch.DiffDelete:
				allLines = append(allLines, Line{
					Op:              OpDelete,
					Text:            text,
					OldLine:         oldLine,
					NewLine:         0,
					DeletedNearLine: newLine,
				})
				fd.DeletedCount++
				// Tag deletion at the current position in the new file
				pos := newLine
				if pos > 1 {
					fd.ChangedLineSet[pos-1] = true
				}
				fd.ChangedLineSet[pos] = true
				oldLine++
			}
		}
	}

	fd.Lines = allLines
	fd.Hunks = buildHunks(allLines, 3)
	return fd
}

func splitLinesPreserve(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func buildHunks(lines []Line, context int) []Hunk {
	var changeIndices []int
	for i, l := range lines {
		if l.Op != OpEqual {
			changeIndices = append(changeIndices, i)
		}
	}
	if len(changeIndices) == 0 {
		return nil
	}

	type group struct {
		start int
		end   int
	}
	var groups []group
	current := group{
		start: maxInt(0, changeIndices[0]-context),
		end:   minInt(len(lines)-1, changeIndices[0]+context),
	}

	for _, idx := range changeIndices[1:] {
		grpStart := maxInt(0, idx-context)
		grpEnd := minInt(len(lines)-1, idx+context)
		if grpStart <= current.end {
			if grpEnd > current.end {
				current.end = grpEnd
			}
		} else {
			groups = append(groups, current)
			current = group{start: grpStart, end: grpEnd}
		}
	}
	groups = append(groups, current)

	var hunks []Hunk
	for _, g := range groups {
		hunkLines := lines[g.start : g.end+1]
		oldStart := 0
		oldCount := 0
		newStart := 0
		newCount := 0

		for _, l := range hunkLines {
			if l.Op == OpEqual || l.Op == OpDelete {
				if oldStart == 0 && l.OldLine > 0 {
					oldStart = l.OldLine
				}
				oldCount++
			}
			if l.Op == OpEqual || l.Op == OpInsert {
				if newStart == 0 && l.NewLine > 0 {
					newStart = l.NewLine
				}
				newCount++
			}
		}

		hunks = append(hunks, Hunk{
			OldStart: oldStart,
			OldCount: oldCount,
			NewStart: newStart,
			NewCount: newCount,
			Lines:    hunkLines,
		})
	}

	return hunks
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Manager coordinates diff baseline storage, computing diffs, and acknowledgements.
type Manager struct {
	ProjectRoot string
	mu          sync.RWMutex
	fileDiffs   map[string]*FileDiff // relPath -> *FileDiff
}

func NewManager(projectRoot string) *Manager {
	return &Manager{
		ProjectRoot: projectRoot,
		fileDiffs:   make(map[string]*FileDiff),
	}
}

// BaselinePath returns the path to the baseline file in .cadr/cache/diff_baseline/
func (m *Manager) BaselinePath(relPath string) string {
	return filepath.Join(m.ProjectRoot, ".cadr", "cache", "diff_baseline", relPath)
}

// GetBaseline retrieves the baseline content for a file.
// Priority:
// 1. .cadr/cache/diff_baseline/<relPath>
// 2. git show HEAD:<relPath>
func (m *Manager) GetBaseline(relPath string) ([]byte, bool, error) {
	baselineFile := m.BaselinePath(relPath)
	if data, err := os.ReadFile(baselineFile); err == nil {
		return data, true, nil
	}

	// Fallback to git show HEAD:<relPath>
	cmd := exec.Command("git", "show", "HEAD:"+relPath)
	cmd.Dir = m.ProjectRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		return stdout.Bytes(), true, nil
	}

	// No baseline found (e.g. untracked file)
	return nil, false, nil
}

// AcknowledgeFile writes the current file content to .cadr/cache/diff_baseline/<relPath>
func (m *Manager) AcknowledgeFile(relPath string) error {
	fullPath := filepath.Join(m.ProjectRoot, relPath)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		// File might have been deleted, remove baseline if exists
		_ = os.Remove(m.BaselinePath(relPath))
		m.mu.Lock()
		delete(m.fileDiffs, relPath)
		m.mu.Unlock()
		return nil
	}

	baselineFile := m.BaselinePath(relPath)
	if err := os.MkdirAll(filepath.Dir(baselineFile), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(baselineFile, content, 0644); err != nil {
		return err
	}

	m.mu.Lock()
	delete(m.fileDiffs, relPath)
	m.mu.Unlock()
	return nil
}

// AcknowledgeFunction patches the baseline file so that only the specified function's lines
// in the current file are accepted, clearing diffs for that function.
func (m *Manager) AcknowledgeFunction(relPath string, fnStart, fnEnd int) error {
	m.mu.RLock()
	fd := m.fileDiffs[relPath]
	m.mu.RUnlock()

	if fd == nil {
		return nil
	}

	// Check if this file has changes outside this function
	hasOtherChanges := false
	for _, l := range fd.Lines {
		if l.Op != OpEqual {
			targetLine := l.NewLine
			if l.Op == OpDelete {
				targetLine = l.DeletedNearLine
			}
			if targetLine < fnStart || targetLine > fnEnd {
				hasOtherChanges = true
				break
			}
		}
	}

	if !hasOtherChanges {
		return m.AcknowledgeFile(relPath)
	}

	// Construct a new baseline: for lines inside [fnStart, fnEnd], take current file content;
	// for lines outside, take baseline content.
	var newBaselineLines []string
	for _, l := range fd.Lines {
		targetLine := l.NewLine
		if l.Op == OpDelete {
			targetLine = l.DeletedNearLine
		}
		inFn := (targetLine >= fnStart && targetLine <= fnEnd)
		if inFn {
			if l.Op == OpEqual || l.Op == OpInsert {
				newBaselineLines = append(newBaselineLines, l.Text)
			}
			// OpDelete inside function is accepted (omitted from new baseline)
		} else {
			if l.Op == OpEqual || l.Op == OpDelete {
				newBaselineLines = append(newBaselineLines, l.Text)
			}
			// OpInsert outside function is omitted (not yet accepted)
		}
	}

	baselineFile := m.BaselinePath(relPath)
	if err := os.MkdirAll(filepath.Dir(baselineFile), 0755); err != nil {
		return err
	}
	content := strings.Join(newBaselineLines, "\n")
	if len(newBaselineLines) > 0 {
		content += "\n"
	}
	if err := os.WriteFile(baselineFile, []byte(content), 0644); err != nil {
		return err
	}

	_ = m.ScanFile(relPath)
	return nil
}

// AcknowledgeDir acknowledges all changed files residing in the directory.
func (m *Manager) AcknowledgeDir(dirRelPath string) error {
	prefix := filepath.Clean(dirRelPath)
	if prefix == "." {
		prefix = ""
	} else if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	m.mu.RLock()
	var targets []string
	for rel := range m.fileDiffs {
		if prefix == "" || strings.HasPrefix(rel, prefix) {
			targets = append(targets, rel)
		}
	}
	m.mu.RUnlock()

	for _, rel := range targets {
		if err := m.AcknowledgeFile(rel); err != nil {
			return err
		}
	}
	return nil
}

// AcknowledgeAll acknowledges all currently detected file diffs.
func (m *Manager) AcknowledgeAll() error {
	m.mu.RLock()
	var targets []string
	for rel := range m.fileDiffs {
		targets = append(targets, rel)
	}
	m.mu.RUnlock()

	for _, rel := range targets {
		if err := m.AcknowledgeFile(rel); err != nil {
			return err
		}
	}
	return nil
}

// ScanFile checks a single file against its baseline.
func (m *Manager) ScanFile(relPath string) *FileDiff {
	fullPath := filepath.Join(m.ProjectRoot, relPath)
	info, err := os.Stat(fullPath)
	if err != nil {
		// File deleted or unreadable
		m.mu.Lock()
		delete(m.fileDiffs, relPath)
		m.mu.Unlock()
		return nil
	}
	if info.IsDir() {
		return nil
	}

	currentBytes, err := os.ReadFile(fullPath)
	if err != nil {
		return nil
	}

	baselineBytes, hasBaseline, _ := m.GetBaseline(relPath)
	var baselineStr string
	if hasBaseline {
		baselineStr = string(baselineBytes)
	}

	currentStr := string(currentBytes)
	if hasBaseline && baselineStr == currentStr {
		m.mu.Lock()
		delete(m.fileDiffs, relPath)
		m.mu.Unlock()
		return nil
	}

	fd := ComputeDiff(baselineStr, currentStr, relPath, fullPath, info.ModTime())
	if fd.AddedCount == 0 && fd.DeletedCount == 0 {
		m.mu.Lock()
		delete(m.fileDiffs, relPath)
		m.mu.Unlock()
		return nil
	}

	m.mu.Lock()
	m.fileDiffs[relPath] = fd
	m.mu.Unlock()
	return fd
}

// ScanAll scans git status and cached baselines to discover all changed files in projectRoot.
func (m *Manager) ScanAll() (map[string]*FileDiff, error) {
	// Check if git is currently locked by an active commit/stage operation
	gitLockPath := filepath.Join(m.ProjectRoot, ".git", "index.lock")
	if _, err := os.Stat(gitLockPath); err == nil {
		m.mu.RLock()
		defer m.mu.RUnlock()
		return m.fileDiffs, nil
	}

	changedSet := make(map[string]bool)

	// 1. Files modified according to git status
	cmd := exec.Command("git", "status", "--porcelain", "-uall")
	cmd.Dir = m.ProjectRoot
	if out, err := cmd.Output(); err == nil {
		lines := strings.Split(string(out), "\n")
		for _, l := range lines {
			if len(l) < 4 {
				continue
			}
			path := strings.TrimSpace(l[3:])
			// handle renamed files "old -> new"
			if idx := strings.Index(path, " -> "); idx != -1 {
				path = path[idx+4:]
			}
			if !strings.HasPrefix(path, ".cadr/") {
				changedSet[path] = true
			}
		}
	} else {
		// If git status failed (e.g. temporary git index lock race), preserve existing diffs
		m.mu.RLock()
		hasExisting := len(m.fileDiffs) > 0
		m.mu.RUnlock()
		if hasExisting {
			m.mu.RLock()
			defer m.mu.RUnlock()
			return m.fileDiffs, nil
		}
	}

	// 2. Check any custom baselines in .cadr/cache/diff_baseline/
	baselineDir := filepath.Join(m.ProjectRoot, ".cadr", "cache", "diff_baseline")
	if _, err := os.Stat(baselineDir); err == nil {
		_ = filepath.Walk(baselineDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(baselineDir, path)
			if err == nil {
				changedSet[rel] = true
			}
			return nil
		})
	}

	// 3. Scan each candidate file
	newDiffs := make(map[string]*FileDiff)
	for rel := range changedSet {
		if fd := m.ScanFile(rel); fd != nil {
			newDiffs[rel] = fd
		}
	}

	m.mu.Lock()
	m.fileDiffs = newDiffs
	m.mu.Unlock()

	return newDiffs, nil
}

// GetFileDiff returns the FileDiff for relPath if it exists.
func (m *Manager) GetFileDiff(relPath string) *FileDiff {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.fileDiffs[relPath]
}

// AllDiffs returns a copy of all current FileDiffs.
func (m *Manager) AllDiffs() map[string]*FileDiff {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make(map[string]*FileDiff, len(m.fileDiffs))
	for k, v := range m.fileDiffs {
		res[k] = v
	}
	return res
}

// FormatRelativeTime converts a modification time into a human-friendly string.
// Examples: "just now", "5m ago", "2h ago", "3 days ago".
func FormatRelativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < 10*time.Second {
		return "just now"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		if m == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", m)
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		if h == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", h)
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "1 day ago"
	}
	return fmt.Sprintf("%d days ago", days)
}
