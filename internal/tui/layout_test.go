package tui

import (
	"strings"
	"testing"

	"github.com/abhinavdevarakonda/cadr/internal/tracer"
	"github.com/charmbracelet/lipgloss"
)

// errModel is a model with everything the bars render, plus a long error.
func errModel(t *testing.T, width, height int) *Model {
	t.Helper()
	m := newTestModel(t)
	m.width, m.height = width, height
	m.rightMode = ModeFlow
	m.languages = "Python"
	m.funcCount = 486
	m.projectPath = "/Users/abee/code/tests/library-management-system-with-a-long-name"
	m.errText = "NameError: name 'x' is not defined · helpers/read_db.py:5"
	m.applyTraceEvent(tracer.Event{Name: "main", File: "/p/main.go", Line: 1, Ev: tracer.EvEnter, Ctx: "1", Span: 1, TS: 0})
	return m
}

// No rendered line may exceed the terminal width. Overflowing the bottom bar used to
// force the layout wider and clip the title bar.
func TestViewFitsWidth(t *testing.T) {
	for _, w := range []int{40, 60, 80, 100, 160} {
		m := errModel(t, w, 40)
		for i, line := range strings.Split(m.View(), "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Fatalf("width %d: line %d is %d cells: %q", w, i, got, line)
			}
		}
	}
}

// The title bar survives a narrow terminal and an error being present.
func TestTitleBarVisibleWhenErrored(t *testing.T) {
	m := errModel(t, 60, 30)
	first := strings.Split(m.View(), "\n")[0]
	if !strings.Contains(first, "cadr") || !strings.Contains(first, "project:") {
		t.Fatalf("title bar missing content: %q", first)
	}
}

// The error belongs in the right pane, not the status bar.
func TestErrorInRightPaneNotStatusBar(t *testing.T) {
	m := errModel(t, 120, 40)
	lines := strings.Split(m.View(), "\n")

	found := -1
	for i, l := range lines {
		if strings.Contains(l, "NameError") {
			if found >= 0 {
				t.Fatalf("error rendered twice: lines %d and %d", found, i)
			}
			found = i
		}
	}
	if found < 0 {
		t.Fatal("error line not rendered at all")
	}
	// the last non-empty line is the status bar
	last := ""
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			last = lines[i]
			break
		}
	}
	if strings.Contains(last, "NameError") {
		t.Fatalf("status bar still carries the error: %q", last)
	}
}

// Reserving the footer row must not make the layout taller.
func TestErrorFooterKeepsLayoutHeight(t *testing.T) {
	plain := errModel(t, 100, 40)
	plain.errText = ""
	withErr := errModel(t, 100, 40)

	if a, b := len(strings.Split(plain.View(), "\n")), len(strings.Split(withErr.View(), "\n")); a != b {
		t.Fatalf("view height changed with the error footer: %d vs %d", a, b)
	}
}
