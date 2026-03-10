package tui

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abhinavdevarakonda/maplet/internal/analyzer"
	"github.com/abhinavdevarakonda/maplet/internal/graph"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	textStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("252"))
	headerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true).Border(lipgloss.NormalBorder(), false, false, true, false).BorderForeground(lipgloss.Color("240"))
	faintStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	paneStyle     = lipgloss.NewStyle().Padding(1, 2)

	dirStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("33")) // Blue
	funcStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("82")) // Green
)

type Mode string

const (
	ModeImpact Mode = "impact"
	ModeTrace  Mode = "trace"
)

type Model struct {
	graph         *graph.Graph
	items         []TreeItem
	expanded      map[string]bool
	selected      int
	width         int
	height        int
	itemToOpen    *TreeItem
	rightMode     Mode
	focus         int // 0: left, 1: right
	rightItems    []analyzer.ImpactResult
	rightSelected int
}

type TreeItem struct {
	ID    string // node ID
	Name  string
	Path  string
	Line  int
	Depth int
	Type  graph.NodeType
	HasC  bool // has children
}

func NewModel(g *graph.Graph) Model {
	m := Model{
		graph:     g,
		expanded:  make(map[string]bool),
		rightMode: ModeImpact,
	}

	// Expand root directory by default (often ".")
	m.expanded["."] = true
	// Expand only the root by default for better clarity
	m.expanded["."] = true
	m.expanded["/"] = true
	// Also expand the first level children of root if they are common dirs
	for id, n := range g.Nodes {
		if n.Type == graph.DirectoryNode && (n.Name == "cmd" || n.Name == "internal" || n.Name == "pkg") {
			m.expanded[id] = true
		}
	}

	m.refreshTree()
	return m
}

func (m *Model) refreshTree() {
	var items []TreeItem

	// Build a tree from contains edges
	children := make(map[string][]*graph.Node)
	roots := make(map[string]*graph.Node)
	hasParent := make(map[string]bool)

	for _, e := range m.graph.Edges {
		if e.Type == graph.ContainsEdge {
			fromNode := m.graph.Nodes[e.From]
			toNode := m.graph.Nodes[e.To]
			if fromNode != nil && toNode != nil {
				children[e.From] = append(children[e.From], toNode)
				hasParent[e.To] = true
			}
		}
	}

	for id, n := range m.graph.Nodes {
		if !hasParent[id] && (n.Type == graph.DirectoryNode || n.Type == graph.FileNode) {
			roots[id] = n
		}
	}

	var rootList []*graph.Node
	for _, n := range roots {
		rootList = append(rootList, n)
	}
	sortNodes(rootList)

	var walk func(n *graph.Node, depth int)
	walk = func(n *graph.Node, depth int) {
		c := children[n.ID]
		sortNodes(c)

		hasC := len(c) > 0

		name := n.Name
		if n.Type == graph.DirectoryNode {
			name += string(filepath.Separator)
		}

		items = append(items, TreeItem{
			ID:    n.ID,
			Name:  name,
			Path:  n.Path,
			Line:  n.Line,
			Depth: depth,
			Type:  n.Type,
			HasC:  hasC,
		})

		if m.expanded[n.ID] {
			for _, child := range c {
				walk(child, depth+1)
			}
		}
	}

	for _, rt := range rootList {
		walk(rt, 0)
	}

	m.items = items
	// ensure bounds
	if m.selected >= len(m.items) {
		m.selected = len(m.items) - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
}

func sortNodes(nodes []*graph.Node) {
	sort.Slice(nodes, func(i, j int) bool {
		// Dirs first, then files, then functions
		if nodes[i].Type != nodes[j].Type {
			weight := func(t graph.NodeType) int {
				switch t {
				case graph.DirectoryNode:
					return 1
				case graph.FileNode:
					return 2
				case graph.FunctionNode:
					return 3
				}
				return 4
			}
			return weight(nodes[i].Type) < weight(nodes[j].Type)
		}
		// same type, sort by line if function, else by name
		if nodes[i].Type == graph.FunctionNode {
			return nodes[i].Line < nodes[j].Line
		}
		return nodes[i].Name < nodes[j].Name
	})
}

func getSignature(path string, line int) string {
	if path == "" || line <= 0 {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	currentLine := 1
	for scanner.Scan() {
		if currentLine == line {
			return strings.TrimSpace(scanner.Text())
		}
		currentLine++
	}
	return ""
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			if m.focus == 0 {
				if m.selected < len(m.items)-1 {
					m.selected++
				}
			} else {
				if m.rightSelected < len(m.rightItems)-1 {
					m.rightSelected++
				}
			}
		case "k", "up":
			if m.focus == 0 {
				if m.selected > 0 {
					m.selected--
				}
			} else {
				if m.rightSelected > 0 {
					m.rightSelected--
				}
			}
		case "h", "left":
			if m.focus == 1 {
				m.focus = 0
			} else {
				item := &m.items[m.selected]
				if m.expanded[item.ID] && item.HasC {
					m.expanded[item.ID] = false
					m.refreshTree()
				} else {
					// move to parent
					for i := m.selected - 1; i >= 0; i-- {
						if m.items[i].Depth < item.Depth {
							m.selected = i
							break
						}
					}
				}
			}
		case "l", "right":
			if m.focus == 0 {
				item := &m.items[m.selected]
				if item.HasC {
					if !m.expanded[item.ID] {
						m.expanded[item.ID] = true
						m.refreshTree()
					} else {
						if m.selected < len(m.items)-1 {
							m.selected++
						}
					}
				} else if item.Type == graph.FunctionNode && len(m.rightItems) > 0 {
					m.focus = 1
					m.rightSelected = 0
				}
			}
		case "i":
			if m.focus == 0 {
				m.focus = 1
				m.rightSelected = 0
			} else {
				m.focus = 0
			}
		case "t":
			if m.rightMode == ModeImpact {
				m.rightMode = ModeTrace
			} else {
				m.rightMode = ModeImpact
			}
		case "enter":
			if m.focus == 0 {
				if m.items[m.selected].Type == graph.FunctionNode || m.items[m.selected].Type == graph.FileNode {
					m.itemToOpen = &m.items[m.selected]
					return m, tea.Quit
				}
			} else {
				if len(m.rightItems) > 0 {
					res := m.rightItems[m.rightSelected]
					n := m.graph.Nodes[res.ID]
					if n != nil {
						m.itemToOpen = &TreeItem{
							ID:   n.ID,
							Path: n.Path,
							Line: res.Line, // Open at call site line
						}
						return m, tea.Quit
					}
				}
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	// Update rightItems based on selection
	if len(m.items) > 0 {
		selectedItem := m.items[m.selected]
		if selectedItem.Type == graph.FunctionNode {
			if m.rightMode == ModeImpact {
				m.rightItems = analyzer.ImpactAnalysis(m.graph, selectedItem.ID)
			} else {
				m.rightItems = analyzer.TraceAnalysis(m.graph, selectedItem.ID)
			}
		} else {
			m.rightItems = nil
		}
	}

	return m, nil
}

func (m Model) View() string {
	if m.width == 0 {
		return "Initializing..."
	}

	// 1. Title bar
	title := faintStyle.Render("Maplet Navigator \u2013 ") + textStyle.Render("project: maplet")
	topBar := lipgloss.NewStyle().
		Width(m.width).
		Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(lipgloss.Color("240")).
		Render(title)

	// Calculate remaining pane height
	paneHeight := m.height - lipgloss.Height(topBar) - 3 // -3 for bottom bar
	if paneHeight < 0 {
		paneHeight = 0
	}

	halfWidth := m.width / 2

	// 2. Left Pane (Structure)
	leftLines := make([]string, 0, paneHeight)
	leftHeader := "Structure"
	if m.focus == 0 {
		leftHeader = "> " + leftHeader
	}
	leftLines = append(leftLines, headerStyle.Width(halfWidth-4).Render(leftHeader))
	leftLines = append(leftLines, "")

	start := 0
	if paneHeight > 4 && len(m.items) > paneHeight-4 {
		if m.selected > paneHeight/2 {
			start = m.selected - paneHeight/2
		}
		if start+paneHeight-4 > len(m.items) {
			start = len(m.items) - (paneHeight - 4)
		}
		if start < 0 {
			start = 0
		}
	}

	for i := start; i < len(m.items) && i < start+paneHeight-4; i++ {
		item := m.items[i]
		indentStr := strings.Repeat("  ", item.Depth)

		var icon string
		switch item.Type {
		case graph.DirectoryNode:
			if m.expanded[item.ID] {
				icon = "🗁 "
			} else {
				icon = "🗀 "
			}
		case graph.FileNode:
			if m.expanded[item.ID] {
				icon = "🗌 "
			} else {
				icon = "🗋 "
			}
		case graph.FunctionNode:
			icon = "ƒ "
		default:
			icon = "  "
		}

		var nameStyle lipgloss.Style
		switch item.Type {
		case graph.DirectoryNode:
			nameStyle = dirStyle
		case graph.FunctionNode:
			nameStyle = funcStyle
		default:
			nameStyle = textStyle
		}

		content := icon + item.Name
		line := indentStr + content

		if i == m.selected && m.focus == 0 {
			// pad to pane width
			padLen := (halfWidth - 4) - lipgloss.Width(line)
			if padLen < 0 {
				padLen = 0
			}
			line = selectedStyle.Render(line + strings.Repeat(" ", padLen))
		} else if i == m.selected {
			// Just a highlight mark without full background if not focused
			line = indentStr + faintStyle.Render(icon) + nameStyle.Underline(true).Render(item.Name)
		} else {
			line = indentStr + faintStyle.Render(icon) + nameStyle.Render(item.Name)
		}
		leftLines = append(leftLines, line)
	}

	for len(leftLines) < paneHeight {
		leftLines = append(leftLines, "")
	}
	leftPaneStr := lipgloss.JoinVertical(lipgloss.Top, leftLines...)
	leftPane := paneStyle.Width(halfWidth).Render(leftPaneStr)

	// 3. Right pane
	rightLines := make([]string, 0, paneHeight)
	rightTitle := "Impact (callers)"
	if m.rightMode == ModeTrace {
		rightTitle = "Trace (callees)"
	}
	if m.focus == 1 {
		rightTitle = "> " + rightTitle
	}
	rightLines = append(rightLines, headerStyle.Width(halfWidth-4).Render(rightTitle))
	rightLines = append(rightLines, "")

	rightStart := 0
	if paneHeight > 4 && len(m.rightItems) > (paneHeight-4)/2 { // /2 because each item has signature
		if m.rightSelected > (paneHeight-4)/4 {
			rightStart = m.rightSelected - (paneHeight-4)/4
		}
		// logic for scrolling right pane... simplified for now
	}

	for i := rightStart; i < len(m.rightItems); i++ {
		if len(rightLines) >= paneHeight-1 {
			break
		}
		res := m.rightItems[i]
		n := m.graph.Nodes[res.ID]

		if n != nil {
			filename := filepath.Base(n.Path)

			name := n.Name
			nameWidth := lipgloss.Width(name)
			padding := ""
			if nameWidth < 20 {
				padding = strings.Repeat(" ", 20-nameWidth)
			}

			funcIcon := "ƒ "
			line1 := fmt.Sprintf("%s%s%s %s %s", funcStyle.Render(funcIcon), funcStyle.Render(name), padding, faintStyle.Render("["+filename+"]"), faintStyle.Render(fmt.Sprintf("(line %d)", res.Line)))
			lineContent := getSignature(n.Path, res.Line)
			line2 := "  " + faintStyle.Italic(true).Render(lineContent)

			if i == m.rightSelected && m.focus == 1 {
				padLen := (halfWidth - 4) - lipgloss.Width(line1)
				if padLen < 0 {
					padLen = 0
				}
				line1 = selectedStyle.Render(line1 + strings.Repeat(" ", padLen))
			}

			rightLines = append(rightLines, line1)
			rightLines = append(rightLines, line2)
		}
	}

	for len(rightLines) < paneHeight {
		rightLines = append(rightLines, "")
	}
	rightPaneStr := lipgloss.JoinVertical(lipgloss.Top, rightLines...)
	rightPane := paneStyle.Width(halfWidth).Render(rightPaneStr)

	// 4. Bottom Bar
	panes := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)

	footerText := " j/k move   h/l expand/focus   i toggle focus   enter open   t trace   q quit"
	bottomBar := lipgloss.NewStyle().
		Width(m.width).
		Border(lipgloss.NormalBorder(), true, false, false, false).
		BorderForeground(lipgloss.Color("240")).
		Foreground(lipgloss.Color("252")).
		Render(footerText)

	return lipgloss.JoinVertical(lipgloss.Top, topBar, panes, bottomBar)
}

func Start(g *graph.Graph) error {
	for {
		m := NewModel(g)
		p := tea.NewProgram(&m, tea.WithAltScreen())

		finalModel, err := p.Run()
		if err != nil {
			return err
		}

		if returnedModel, ok := finalModel.(Model); ok {
			if returnedModel.itemToOpen != nil {
				sttyOutput, _ := exec.Command("stty", "-g").Output()

				editor := os.Getenv("EDITOR")
				if editor == "" {
					editor = "nvim"
				}

				var cmd *exec.Cmd
				if strings.Contains(editor, "vim") || strings.Contains(editor, "nvim") {
					cmd = exec.Command(editor, fmt.Sprintf("+%d", returnedModel.itemToOpen.Line), returnedModel.itemToOpen.Path)
				} else if strings.Contains(editor, "code") {
					cmd = exec.Command(editor, "-g", fmt.Sprintf("%s:%d", returnedModel.itemToOpen.Path, returnedModel.itemToOpen.Line))
				} else {
					cmd = exec.Command(editor, returnedModel.itemToOpen.Path)
				}

				cmd.Stdin = os.Stdin
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr

				if err := cmd.Run(); err != nil {
					fmt.Fprintf(os.Stderr, "Error running editor: %v\n", err)
				}

				if len(sttyOutput) > 0 {
					exec.Command("stty", string(sttyOutput)).Run()
				}

				continue // go back to the TUI
			}
		}

		return nil
	}
}
