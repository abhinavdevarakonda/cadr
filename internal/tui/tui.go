package tui

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/abhinavdevarakonda/maplet/internal/analyzer"
	"github.com/abhinavdevarakonda/maplet/internal/graph"
	"github.com/abhinavdevarakonda/maplet/internal/tracer"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	textStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("252"))
	headerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true).Border(lipgloss.NormalBorder(), false, false, true, false).BorderForeground(lipgloss.Color("240"))
	faintStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	paneStyle     = lipgloss.NewStyle().Padding(1, 2)

	dirStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("33"))             // Blue
	funcStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))             // Green
	glowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true) // Yellow

	// Heatmap Styles
	heatLow     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))  // Soft Green
	heatMed     = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // Warm Orange
	heatHigh    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // Vivid Red
	heatBlazing = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true).Underline(true)
)

type Mode string

const (
	ModeImpact Mode = "impact"
	ModeTrace  Mode = "trace"
	ModeFlow   Mode = "flow"
)

type TraceEventMsg tracer.Event

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

	// Flow & Heatmap fields
	history      []tracer.Event
	playhead     int
	isLive       bool
	isMonitoring bool
	hitCounts    map[string]int // ID -> count
	rightScroll  int            // Add offset state for Flow pane scrolling
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
		isLive:    true,
		hitCounts: make(map[string]int),
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

func (m *Model) syncToHistory() {
	if len(m.history) == 0 || m.playhead >= len(m.history) {
		return
	}
	msg := m.history[m.playhead]

	// 1. Find the node ID in the graph
	var targetNodeID string
	for id, n := range m.graph.Nodes {
		if n.Type == graph.FunctionNode && (n.Name == msg.Name || strings.HasSuffix(msg.Name, "."+n.Name)) && strings.HasSuffix(msg.File, n.Path) {
			targetNodeID = id
			break
		}
	}

	if targetNodeID != "" {
		// 2. Auto-Expand all parents
		curr := targetNodeID
		for {
			parentID := ""
			for _, e := range m.graph.Edges {
				if e.To == curr && e.Type == graph.ContainsEdge {
					parentID = e.From
					break
				}
			}
			if parentID == "" {
				break
			}
			m.expanded[parentID] = true
			curr = parentID
		}

		m.refreshTree()

		// 3. Select the item
		for i, item := range m.items {
			if item.ID == targetNodeID {
				m.selected = i
				break
			}
		}
	}
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
		const jump = 5

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			if m.focus == 0 {
				if m.selected < len(m.items)-1 {
					m.selected++
				}
			} else {
				if m.rightMode == ModeFlow {
					if len(m.history) > 0 && m.playhead < len(m.history)-1 {
						m.playhead++
						m.syncToHistory()
					}
				} else {
					if m.rightSelected < len(m.rightItems)-1 {
						m.rightSelected++
					}
				}
			}
		case "k", "up":
			if m.focus == 0 {
				if m.selected > 0 {
					m.selected--
				}
			} else {
				if m.rightMode == ModeFlow {
					if len(m.history) > 0 && m.playhead > 0 {
						m.playhead--
						m.isLive = false
						m.syncToHistory()
					}
				} else {
					if m.rightSelected > 0 {
						m.rightSelected--
					}
				}
			}
		case "ctrl+j":
			if m.focus == 0 {
				m.selected += jump
				if m.selected >= len(m.items) {
					m.selected = len(m.items) - 1
				}
			} else {
				if m.rightMode == ModeFlow {
					if len(m.history) > 0 {
						m.playhead += jump
						if m.playhead >= len(m.history) {
							m.playhead = len(m.history) - 1
						}
						m.syncToHistory()
					}
				} else {
					m.rightSelected += jump
					if m.rightSelected >= len(m.rightItems) {
						m.rightSelected = len(m.rightItems) - 1
					}
				}
			}
		case "ctrl+k":
			if m.focus == 0 {
				m.selected -= jump
				if m.selected < 0 {
					m.selected = 0
				}
			} else {
				if m.rightMode == ModeFlow {
					if len(m.history) > 0 {
						m.playhead -= jump
						if m.playhead < 0 {
							m.playhead = 0
						}
						m.isLive = false
						m.syncToHistory()
					}
				} else {
					m.rightSelected -= jump
					if m.rightSelected < 0 {
						m.rightSelected = 0
					}
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
			} else if m.rightMode == ModeTrace {
				m.rightMode = ModeFlow
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
				if m.rightMode == ModeFlow {
					if len(m.history) > 0 && m.playhead < len(m.history) {
						hit := m.history[m.playhead]

						// Try to resolve it perfectly to a known graph node
						found := false
						for id, n := range m.graph.Nodes {
							if n.Type == graph.FunctionNode && (n.Name == hit.Name || strings.HasSuffix(hit.Name, "."+n.Name)) && strings.HasSuffix(hit.File, n.Path) {
								m.itemToOpen = &TreeItem{
									ID:   id,
									Path: n.Path,
									Line: n.Line,
								}
								found = true
								break
							}
						}

						// Fallback: If it's a class or internal Python module not in the static graph, open it anyway!
						if !found {
							m.itemToOpen = &TreeItem{
								ID:   "dynamic-fallback",
								Path: hit.File,
								Line: hit.Line,
							}
						}

						return m, tea.Quit
					}
				} else if len(m.rightItems) > 0 {
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
		case " ":
			m.isLive = !m.isLive
		case "H":
			if len(m.history) > 0 && m.playhead > 0 {
				m.playhead--
				m.isLive = false
				m.syncToHistory()
			}
		case "L":
			if len(m.history) > 0 && m.playhead < len(m.history)-1 {
				m.playhead++
				m.syncToHistory()
			}
		}
	case TraceEventMsg:
		m.history = append(m.history, tracer.Event(msg))

		// Update Heatmap
		for id, n := range m.graph.Nodes {
			if n.Type == graph.FunctionNode && (n.Name == msg.Name || strings.HasSuffix(msg.Name, "."+n.Name)) && strings.HasSuffix(msg.File, n.Path) {
				m.hitCounts[id]++
				break
			}
		}

		if m.isLive {
			m.playhead = len(m.history) - 1
			m.syncToHistory()
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
			// Apply Heatmap Style
			count := m.hitCounts[item.ID]
			if count > 100 {
				nameStyle = heatBlazing
			} else if count > 20 {
				nameStyle = heatHigh
			} else if count > 5 {
				nameStyle = heatMed
			} else if count > 0 {
				nameStyle = heatLow
			} else {
				nameStyle = funcStyle
			}
		default:
			nameStyle = textStyle
		}

		// YELLOW GLOW: Is this the current hit?
		isHit := false
		if len(m.history) > 0 && m.playhead < len(m.history) {
			hit := m.history[m.playhead]
			if item.Name == hit.Name || strings.HasSuffix(hit.Name, "."+item.Name) {
				if item.Type == graph.FunctionNode {
					isHit = true
				}
			}
		}

		content := icon + item.Name
		line := indentStr + content

		if isHit {
			if i == m.selected && m.focus == 0 {
				// Combined Highlight: Reverse color or thick mark
				line = indentStr + glowStyle.Bold(true).Render("▶ "+content)
			} else {
				// The glowing active hit should be the boldest version of its heat color
				baseColor := nameStyle.GetForeground()
				if baseColor == lipgloss.Color("") {
					baseColor = lipgloss.Color("220")
				}
				line = indentStr + lipgloss.NewStyle().Foreground(baseColor).Bold(true).Background(lipgloss.Color("235")).Render(icon+item.Name)
			}
		} else if i == m.selected && m.focus == 0 {
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
	rightTitle := "Impact"
	switch m.rightMode {
	case ModeImpact:
		rightTitle = "Impact (callers)"
	case ModeTrace:
		rightTitle = "Trace (callees)"
	case ModeFlow:
		rightTitle = "Flow (sequence)"
	}

	if m.focus == 1 {
		rightTitle = "> " + rightTitle
	}
	rightLines = append(rightLines, headerStyle.Width(halfWidth-4).Render(rightTitle))
	rightLines = append(rightLines, "")

	if m.rightMode == ModeFlow {
		visibleCount := paneHeight - 4

		// 1. Maintain camera tracking bounds
		if m.playhead < m.rightScroll {
			m.rightScroll = m.playhead
		} else if m.playhead >= m.rightScroll+visibleCount {
			m.rightScroll = m.playhead - visibleCount + 1
		}

		// 2. Bound checks
		if m.rightScroll < 0 {
			m.rightScroll = 0
		}

		endHit := m.rightScroll + visibleCount
		if endHit > len(m.history) {
			endHit = len(m.history)
		}

		for i := m.rightScroll; i < endHit; i++ {
			hit := m.history[i]
			prefix := "  "
			if i == m.playhead {
				prefix = "> "
			}
			line := fmt.Sprintf("%s#%d %s", prefix, i+1, hit.Name)
			if i == m.playhead {
				line = glowStyle.Render(line)
			}
			rightLines = append(rightLines, line)
		}
	} else if len(m.rightItems) > 0 {
		for i := 0; i < len(m.rightItems); i++ {
			if len(rightLines) >= paneHeight-1 {
				break
			}
			res := m.rightItems[i]
			n := m.graph.Nodes[res.ID]
			if n != nil {
				filename := filepath.Base(n.Path)
				line1 := fmt.Sprintf("%s%s %s %s", funcStyle.Render("ƒ "), funcStyle.Render(n.Name), faintStyle.Render("["+filename+"]"), faintStyle.Render(fmt.Sprintf("(line %d)", res.Line)))
				line2 := "  " + faintStyle.Italic(true).Render(getSignature(n.Path, res.Line))
				if i == m.rightSelected && m.focus == 1 {
					line1 = selectedStyle.Render(line1 + strings.Repeat(" ", 15))
				}
				rightLines = append(rightLines, line1, line2)
			}
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
	if len(m.history) > 0 {
		status := ""
		if m.isMonitoring {
			status = lipgloss.NewStyle().Foreground(lipgloss.Color("160")).Render("● RECORDING")
			if !m.isLive {
				status = lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Render("○ PAUSED")
			}
			status += " | "
		}
		footerText = fmt.Sprintf(" %sHit %d/%d | H/L scrub | Space live | ", status, m.playhead+1, len(m.history)) + footerText
	}
	bottomBar := lipgloss.NewStyle().
		Width(m.width).
		Border(lipgloss.NormalBorder(), true, false, false, false).
		BorderForeground(lipgloss.Color("240")).
		Foreground(lipgloss.Color("252")).
		Render(footerText)

	return lipgloss.JoinVertical(lipgloss.Top, topBar, panes, bottomBar)
}

func openEditor(item *TreeItem) error {
	sttyOutput, _ := exec.Command("stty", "-g").Output()
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "nvim"
	}
	var cmd *exec.Cmd
	if strings.Contains(editor, "vim") || strings.Contains(editor, "nvim") {
		cmd = exec.Command(editor, fmt.Sprintf("+%d", item.Line), item.Path)
	} else if strings.Contains(editor, "code") {
		cmd = exec.Command(editor, "-g", fmt.Sprintf("%s:%d", item.Path, item.Line))
	} else {
		cmd = exec.Command(editor, item.Path)
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
	return nil
}

func Start(g *graph.Graph) error {
	m := NewModel(g)

	// Passive Listening: Nav listens but doesn't error if port is busy (another Maplet might be open)
	var prog *tea.Program
	go func() {
		tracer.Listen(func(e tracer.Event) {
			if prog != nil {
				prog.Send(TraceEventMsg(e))
			}
		})
	}()

	for {
		prog = tea.NewProgram(&m, tea.WithAltScreen())
		finalModel, err := prog.Run()
		if err != nil {
			return err
		}
		if returnedModel, ok := finalModel.(Model); ok {
			m = returnedModel
			if m.itemToOpen != nil {
				openEditor(m.itemToOpen)
				m.itemToOpen = nil
				continue
			}
		}
		return nil
	}
}

func StartMonitor(g *graph.Graph, target string) error {
	m := NewModel(g)
	m.isMonitoring = true
	m.rightMode = ModeFlow

	// Active Listening: Error if port is busy
	var prog *tea.Program
	go func() {
		if err := tracer.Listen(func(e tracer.Event) {
			if prog != nil {
				prog.Send(TraceEventMsg(e))
			}
		}); err != nil {
			fmt.Fprintf(os.Stderr, "Error: monitor could not start on port 9876: %v\n", err)
			os.Exit(1)
		}
	}()

	// Optionally start the target
	if target != "" {
		go func() {
			time.Sleep(100 * time.Millisecond)
			tracer.Run(target, func(e tracer.Event) {
				if prog != nil {
					prog.Send(TraceEventMsg(e))
				}
			})
		}()
	}

	for {
		prog = tea.NewProgram(&m, tea.WithAltScreen())
		finalModel, err := prog.Run()
		if err != nil {
			return err
		}
		if returnedModel, ok := finalModel.(Model); ok {
			m = returnedModel
			if m.itemToOpen != nil {
				openEditor(m.itemToOpen)
				m.itemToOpen = nil
				continue
			}
		}
		return nil
	}
}
