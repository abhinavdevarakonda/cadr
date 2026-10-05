package tui

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/abhinavdevarakonda/cadr/internal/analyzer"
	"github.com/abhinavdevarakonda/cadr/internal/diff"
	"github.com/abhinavdevarakonda/cadr/internal/graph"
	"github.com/abhinavdevarakonda/cadr/internal/tracer"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// brightYellow is the highlight colour shared by the active-function marker and
// the diff/language accents.
const brightYellow = lipgloss.Color("3")

var (
	textStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	selectedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("252"))
	headerStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true).Border(lipgloss.NormalBorder(), false, false, true, false).BorderForeground(lipgloss.Color("240"))
	faintStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	diffBadgeStyle = lipgloss.NewStyle().Foreground(brightYellow)
	paneStyle      = lipgloss.NewStyle().Padding(1, 2)
	dirIconStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#A1A578")) // sage white folder icons
	fileIconStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#AFB3A7")) // light file icons

	/* palette: jellybeans
	dirStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("103"))            // blue
	funcStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("107"))            // green
	glowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("222")).Bold(true) // yellow
	*/

	// adaptive (follows your terminal theme exactly)
	dirStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))     // blue
	funcStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))     // green
	glowStyle = lipgloss.NewStyle().Foreground(brightYellow).Bold(true) // yellow

	// heatmap styles
	heatLow     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))  // Soft Green
	heatMed     = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // Warm Orange
	heatHigh    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")) // Vivid Red
	heatBlazing = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true).Underline(true)

	// icon config
	useNerdIcons = true

	iconOpen   = "[-] "
	iconClosed = "[+] "
	iconFile   = ". "
	iconFunc   = "f "

	nerdOpen   = "\uf07c "
	nerdClosed = "\uf07b "
	nerdFile   = "\uf15b "
	nerdFunc   = "ƒ "
)

type Mode string

const (
	ModePreview Mode = "preview"
	ModeImpact  Mode = "impact"
	ModeFlow    Mode = "flow"
)

type TraceEventMsg tracer.Event
type TraceBatchMsg []tracer.Event
type DiffUpdatedMsg struct{}

type Model struct {
	graph         *graph.Graph
	items         []TreeItem
	expanded      map[string]bool
	selected      int
	width         int
	height        int
	itemToOpen    *TreeItem
	rightMode     Mode
	impactCallees bool // false: callers, true: callees
	previewScroll int
	focus         int // 0: left, 1: right
	rightItems    []analyzer.ImpactResult
	rightSelected int

	// Flow & Heatmap fields
	history      []tracer.Event
	playhead     int
	isLive       bool
	isMonitoring bool
	hitCounts    map[string]int // ID -> count
	rightScroll  int

	// Search & QoL
	searching   bool
	searchText  string
	followLive  bool
	pendingG    bool
	oldExpanded map[string]bool // Backups for "c" toggle
	showHelp    bool

	// Status bar info (computed once)
	languages   string
	funcCount   int
	projectPath string

	// Diff tracking
	projectRoot string
	diffMgr     *diff.Manager
	diffMap     map[string]*diff.FileDiff
}

type TreeItem struct {
	ID        string // node ID
	Name      string
	Path      string
	Line      int
	Depth     int
	Type      graph.NodeType
	HasC      bool   // has children
	HasDiff   bool   // has unacknowledged diff
	DiffAge   string // e.g. "5m ago"
	ShowBadge bool   // true if badge should be shown on this tree row (suppressed when expanded)
}

func NewModel(g *graph.Graph, projectRoot string) Model {
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		absRoot = projectRoot
	}

	m := Model{
		graph:         g,
		projectRoot:   absRoot,
		expanded:      make(map[string]bool),
		rightMode:     ModePreview,
		impactCallees: false,
		isLive:        true,
		followLive:    true,
		hitCounts:     make(map[string]int),
		diffMgr:       diff.NewManager(absRoot),
		diffMap:       make(map[string]*diff.FileDiff),
	}

	if diffs, err := m.diffMgr.ScanAll(); err == nil {
		m.diffMap = diffs
	}

	// Resolve the project name from the given root path
	if projectRoot == "." || projectRoot == "" {
		if wd, err := os.Getwd(); err == nil {
			m.projectPath = filepath.Base(wd)
		} else {
			m.projectPath = "."
		}
	} else {
		absPath, err := filepath.Abs(projectRoot)
		if err == nil {
			m.projectPath = filepath.Base(absPath)
		} else {
			m.projectPath = filepath.Base(projectRoot)
		}
	}

	// Compute stats for status bar
	langSet := make(map[string]bool)
	for _, n := range g.Nodes {
		if n.Type == graph.FunctionNode {
			m.funcCount++
		}
		if n.Type == graph.FileNode {
			ext := filepath.Ext(n.Name)
			switch ext {
			case ".py":
				langSet["Python"] = true
			case ".go":
				langSet["Go"] = true
			case ".js":
				langSet["JavaScript"] = true
			case ".ts":
				langSet["TypeScript"] = true
			case ".rb":
				langSet["Ruby"] = true
			case ".rs":
				langSet["Rust"] = true
			case ".java":
				langSet["Java"] = true
			}
		}
	}
	langs := make([]string, 0, len(langSet))
	for l := range langSet {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	m.languages = strings.Join(langs, ", ")
	if m.languages == "" {
		m.languages = "Unknown"
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

		hasDiff := false
		showBadge := false
		diffAge := ""
		relPath := filepath.Clean(n.Path)

		if m.diffMap != nil {
			switch n.Type {
			case graph.FileNode:
				if fd := m.diffMap[relPath]; fd != nil {
					hasDiff = true
					diffAge = diff.FormatRelativeTime(fd.ModTime)
					if !m.expanded[n.ID] || !hasC {
						showBadge = true
					}
				}
			case graph.FunctionNode:
				if fd := m.diffMap[relPath]; fd != nil {
					if fd.FunctionHasDiff(n.Line, n.EndLine) {
						hasDiff = true
						showBadge = true
						diffAge = diff.FormatRelativeTime(fd.ModTime)
					}
				}
			case graph.DirectoryNode:
				prefix := relPath
				if prefix == "." {
					prefix = ""
				} else if !strings.HasSuffix(prefix, "/") {
					prefix += "/"
				}
				var latest time.Time
				for dRel, fd := range m.diffMap {
					if fd == nil {
						continue
					}
					if prefix == "" || strings.HasPrefix(dRel, prefix) {
						hasDiff = true
						if fd.ModTime.After(latest) {
							latest = fd.ModTime
						}
					}
				}
				if hasDiff {
					diffAge = diff.FormatRelativeTime(latest)
					if !m.expanded[n.ID] {
						showBadge = true
					}
				}
			}
		}

		items = append(items, TreeItem{
			ID:        n.ID,
			Name:      name,
			Path:      n.Path,
			Line:      n.Line,
			Depth:     depth,
			Type:      n.Type,
			HasC:      hasC,
			HasDiff:   hasDiff,
			DiffAge:   diffAge,
			ShowBadge: showBadge,
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

func (m *Model) applyTraceEvent(e tracer.Event) {
	m.history = append(m.history, e)

	// Update Heatmap
	for id, n := range m.graph.Nodes {
		if n.Type == graph.FunctionNode && (n.Name == e.Name || strings.HasSuffix(e.Name, "."+n.Name)) && strings.HasSuffix(e.File, n.Path) {
			m.hitCounts[id]++
			break
		}
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		const jump = 5
		key := msg.String()

		if m.pendingG {
			m.pendingG = false
			if key == "g" {
				m.selected = 0
				m.playhead = 0
				m.syncToHistory()
				return m, nil
			}
		}

		if m.searching {
			switch key {
			case "enter", "esc":
				m.searching = false
			case "backspace":
				if len(m.searchText) > 0 {
					m.searchText = m.searchText[:len(m.searchText)-1]
				}
			default:
				if len(key) == 1 {
					m.searchText += key
					query := strings.ToLower(m.searchText)
					if query == "" {
						break
					}

					if m.focus == 0 {
						// Search GRAPH (Deep search, find then expand ancestors)
						var matchID string
						for id, n := range m.graph.Nodes {
							if strings.Contains(strings.ToLower(n.Name), query) {
								matchID = id
								break
							}
						}

						if matchID != "" {
							// Expand all ancestors
							curr := matchID
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
							for i, item := range m.items {
								if item.ID == matchID {
									m.selected = i
									break
								}
							}
						}
					} else if m.focus == 1 && m.rightMode == ModeFlow {
						// Search HISTORY (Jump playhead)
						for i := 0; i < len(m.history); i++ {
							if strings.Contains(strings.ToLower(m.history[i].Name), query) {
								m.playhead = i
								m.isLive = false
								m.syncToHistory()
								break
							}
						}
					}
				}
			}
			return m, nil
		}

		switch key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "?":
			m.showHelp = !m.showHelp
			return m, nil
		case "/":
			if m.showHelp {
				m.showHelp = false
				return m, nil
			}
			m.searching = true
			m.searchText = ""
		case "g":
			m.pendingG = true
		case "f":
			m.followLive = !m.followLive
		case "j", "down":
			if m.focus == 0 {
				if m.selected < len(m.items)-1 {
					m.selected++
					m.previewScroll = 0
				}
			} else {
				if m.rightMode == ModeFlow {
					if len(m.history) > 0 && m.playhead < len(m.history)-1 {
						m.playhead++
						m.syncToHistory()
					}
				} else if m.rightMode == ModePreview {
					m.previewScroll++
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
					m.previewScroll = 0
				}
			} else {
				if m.rightMode == ModeFlow {
					if len(m.history) > 0 && m.playhead > 0 {
						m.playhead--
						m.isLive = false
						m.syncToHistory()
					}
				} else if m.rightMode == ModePreview {
					if m.previewScroll > 0 {
						m.previewScroll--
					}
				} else {
					if m.rightSelected > 0 {
						m.rightSelected--
					}
				}
			}
		case "ctrl+j":
			if m.focus == 0 {
				if len(m.items) > 0 {
					m.selected += jump
					if m.selected >= len(m.items) {
						m.selected = len(m.items) - 1
					}
					m.previewScroll = 0
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
				} else if m.rightMode == ModePreview {
					m.previewScroll += jump
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
				m.previewScroll = 0
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
				} else if m.rightMode == ModePreview {
					m.previewScroll -= jump
					if m.previewScroll < 0 {
						m.previewScroll = 0
					}
				} else {
					m.rightSelected -= jump
					if m.rightSelected < 0 {
						m.rightSelected = 0
					}
				}
			}
		case "G":
			if m.focus == 0 {
				if len(m.items) > 0 {
					m.selected = len(m.items) - 1
					m.previewScroll = 0
				}
			} else {
				m.playhead = len(m.history) - 1
			}
		case "c": // c: Smart Collapse/Restore Toggle
			isMainlyCollapsed := true
			for id, expanded := range m.expanded {
				if id != "." && id != "/" && expanded {
					isMainlyCollapsed = false
					break
				}
			}

			if isMainlyCollapsed {
				if len(m.oldExpanded) > 0 {
					m.expanded = m.oldExpanded
					m.oldExpanded = nil
				} else {
					for id, n := range m.graph.Nodes {
						if n.Type == graph.DirectoryNode && !strings.Contains(n.Path, string(os.PathSeparator)) {
							m.expanded[id] = true
						}
					}
				}
			} else {
				m.oldExpanded = make(map[string]bool)
				for k, v := range m.expanded {
					m.oldExpanded[k] = v
				}
				m.expanded = make(map[string]bool)
				m.expanded["."] = true
				m.selected = 0
				m.previewScroll = 0
			}
			m.refreshTree()

		case "h", "left":
			if m.focus == 1 {
				m.focus = 0
			} else if len(m.items) > 0 {
				item := &m.items[m.selected]
				if m.expanded[item.ID] && item.HasC {
					m.expanded[item.ID] = false
					m.refreshTree()
				} else {
					// move to parent
					for i := m.selected - 1; i >= 0; i-- {
						if m.items[i].Depth < item.Depth {
							m.selected = i
							m.previewScroll = 0
							break
						}
					}
				}
			}
		case "l", "right":
			if m.focus == 0 && len(m.items) > 0 {
				item := &m.items[m.selected]
				if item.HasC {
					if !m.expanded[item.ID] {
						m.expanded[item.ID] = true
						m.refreshTree()
					} else {
						if m.selected < len(m.items)-1 {
							m.selected++
							m.previewScroll = 0
						}
					}
				} else if item.Type == graph.FunctionNode || item.Type == graph.FileNode {
					m.focus = 1
					if m.rightMode == ModeImpact {
						m.rightSelected = 0
					}
				}
			}
		case "i":
			if m.focus == 0 {
				m.focus = 1
				m.rightSelected = 0
			} else {
				m.focus = 0
			}
		case "t", "tab":
			switch m.rightMode {
			case ModePreview:
				m.rightMode = ModeImpact
			case ModeImpact:
				m.rightMode = ModeFlow
			case ModeFlow:
				m.rightMode = ModePreview
			default:
				m.rightMode = ModePreview
			}
			m.rightSelected = 0
			m.updateRightItems()
		case "a":
			if len(m.items) > 0 && m.diffMgr != nil {
				item := m.items[m.selected]
				switch item.Type {
				case graph.FunctionNode:
					n := m.graph.Nodes[item.ID]
					if n != nil {
						_ = m.diffMgr.AcknowledgeFunction(n.Path, n.Line, n.EndLine)
					}
				case graph.FileNode:
					_ = m.diffMgr.AcknowledgeFile(item.Path)
				case graph.DirectoryNode:
					_ = m.diffMgr.AcknowledgeDir(item.Path)
				}
				if diffs, err := m.diffMgr.ScanAll(); err == nil {
					m.diffMap = diffs
				}
				m.previewScroll = 0
				m.refreshTree()
			}
		case "A":
			if m.diffMgr != nil {
				_ = m.diffMgr.AcknowledgeAll()
				if diffs, err := m.diffMgr.ScanAll(); err == nil {
					m.diffMap = diffs
				}
				m.previewScroll = 0
				m.refreshTree()
			}
		case "enter":
			if m.focus == 0 {
				if len(m.items) == 0 {
					break
				}
				item := m.items[m.selected]
				if item.Type == graph.DirectoryNode {
					m.expanded[item.ID] = !m.expanded[item.ID]
					m.refreshTree()
				} else if item.Type == graph.FunctionNode || item.Type == graph.FileNode {
					m.itemToOpen = &m.items[m.selected]
					return m, tea.Quit
				}
			} else {
				if m.rightMode == ModePreview {
					if len(m.items) > 0 {
						m.itemToOpen = &m.items[m.selected]
						return m, tea.Quit
					}
				} else if m.rightMode == ModeFlow {
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
						}
						if !m.impactCallees {
							m.itemToOpen.Line = res.Line // call site in caller's file
						} else {
							m.itemToOpen.Line = n.Line // callee's definition
						}
						return m, tea.Quit
					}
				}
			}
		case " ":
			if m.rightMode == ModeImpact {
				m.impactCallees = !m.impactCallees
				m.rightSelected = 0
				m.updateRightItems()
			} else if m.rightMode == ModeFlow {
				m.isLive = !m.isLive
			}
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
		m.applyTraceEvent(tracer.Event(msg))
		if m.isLive {
			m.playhead = len(m.history) - 1
			if m.followLive {
				m.syncToHistory()
			}
		}
	case TraceBatchMsg:
		for _, e := range msg {
			m.applyTraceEvent(e)
		}
		if m.isLive && len(msg) > 0 {
			m.playhead = len(m.history) - 1
			if m.followLive {
				m.syncToHistory()
			}
		}
	case DiffUpdatedMsg:
		if m.diffMgr != nil {
			func() {
				defer func() {
					_ = recover()
				}()
				if diffs, err := m.diffMgr.ScanAll(); err == nil {
					m.diffMap = diffs
				}
				m.refreshTree()
			}()
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	}

	// Update rightItems based on selection
	m.updateRightItems()

	return m, nil
}

func (m Model) View() string {
	if m.width == 0 {
		return "Initializing..."
	}

	// 1. Title bar
	title := faintStyle.Render("cadr") + faintStyle.Render(" | ") + textStyle.Render("project: "+m.projectPath)
	topBar := lipgloss.NewStyle().
		Width(m.width).
		Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(lipgloss.Color("240")).
		Render(title)

	// calculate remaining pane height
	paneHeight := m.height - lipgloss.Height(topBar) - 5
	if paneHeight < 0 {
		paneHeight = 0
	}

	halfWidth := m.width / 2
	rightWidth := m.width - halfWidth
	contentWidth := halfWidth - 4
	if contentWidth < 10 {
		contentWidth = 10
	}
	rightContentWidth := rightWidth - 4
	if rightContentWidth < 10 {
		rightContentWidth = 10
	}
	clipStyle := lipgloss.NewStyle().MaxWidth(contentWidth)

	// 2. Left Pane (Structure)
	leftLines := make([]string, 0, paneHeight)
	leftHeader := "Structure"
	if m.focus == 0 {
		leftHeader = "> " + leftHeader
	}
	leftLines = append(leftLines, headerStyle.Width(contentWidth).Render(leftHeader))
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

	if len(m.items) == 0 {
		leftLines = append(leftLines, clipStyle.Render("  "+faintStyle.Render("No source files found")))
	}

	for i := start; i < len(m.items) && i < start+paneHeight-4; i++ {
		item := m.items[i]
		indentStr := strings.Repeat("  ", item.Depth)

		var icon string
		if useNerdIcons {
			switch item.Type {
			case graph.DirectoryNode:
				if m.expanded[item.ID] {
					icon = nerdOpen
				} else {
					icon = nerdClosed
				}
			case graph.FileNode:
				icon = nerdFile
			case graph.FunctionNode:
				icon = nerdFunc
			default:
				icon = "  "
			}
		} else {
			switch item.Type {
			case graph.DirectoryNode:
				if m.expanded[item.ID] {
					icon = iconOpen
				} else {
					icon = iconClosed
				}
			case graph.FileNode:
				icon = iconFile
			case graph.FunctionNode:
				icon = iconFunc
			default:
				icon = "  "
			}
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
		case graph.FileNode:
			if isCodeFile(item.Name) {
				nameStyle = textStyle
			} else {
				nameStyle = faintStyle
			}
		default:
			nameStyle = textStyle
		}

		// Folder and file icons use brighter accents; other rows stay faint.
		iconStyle := faintStyle
		switch item.Type {
		case graph.DirectoryNode:
			iconStyle = dirIconStyle
		case graph.FileNode:
			iconStyle = fileIconStyle
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

		plainLeft := indentStr + icon + item.Name
		leftWidth := lipgloss.Width(plainLeft)
		if leftWidth > contentWidth {
			leftWidth = contentWidth
		}

		var line string
		isSelected := (i == m.selected && m.focus == 0)

		if item.ShowBadge {
			age := item.DiffAge
			if age == "" {
				age = "changed"
			}
			badgePlain := age + " ~"
			badgeWidth := lipgloss.Width(badgePlain)

			// Space for dotted connector
			avail := contentWidth - leftWidth - badgeWidth
			if avail < 1 {
				avail = 1
			}

			connector := strings.Repeat("·", avail)
			if avail >= 2 {
				connector = " " + strings.Repeat("·", avail-2) + " "
			}

			if isSelected {
				// FULL SOLID WHITE BAR: unstyled text so background color is never canceled mid-line
				row := plainLeft + connector + badgePlain
				padLen := contentWidth - lipgloss.Width(row)
				if padLen > 0 {
					row += strings.Repeat(" ", padLen)
				}
				line = selectedStyle.Render(row)
			} else {
				styledLeft := indentStr + iconStyle.Render(icon) + nameStyle.Render(item.Name)
				if isHit {
					baseColor := nameStyle.GetForeground()
					if baseColor == lipgloss.Color("") {
						baseColor = lipgloss.Color("220")
					}
					styledLeft = indentStr + lipgloss.NewStyle().Foreground(baseColor).Bold(true).Background(lipgloss.Color("235")).Render(icon+item.Name)
				}
				styledConnector := faintStyle.Render(connector)
				styledBadge := faintStyle.Render(age) + " " + diffBadgeStyle.Render("~")
				line = styledLeft + styledConnector + styledBadge
			}
		} else {
			padLen := contentWidth - leftWidth
			if padLen < 0 {
				padLen = 0
			}

			if isSelected {
				// FULL SOLID WHITE BAR
				row := plainLeft + strings.Repeat(" ", padLen)
				line = selectedStyle.Render(row)
			} else if i == m.selected {
				line = indentStr + iconStyle.Render(icon) + nameStyle.Underline(true).Render(item.Name)
			} else if isHit {
				line = indentStr + glowStyle.Bold(true).Render("▶ "+icon+item.Name)
			} else {
				line = indentStr + iconStyle.Render(icon) + nameStyle.Render(item.Name)
			}
		}
		leftLines = append(leftLines, clipStyle.Render(line))
	}

	if len(leftLines) > paneHeight {
		leftLines = leftLines[:paneHeight]
	}
	for len(leftLines) < paneHeight {
		leftLines = append(leftLines, "")
	}
	leftPaneStr := lipgloss.JoinVertical(lipgloss.Top, leftLines...)
	leftPane := paneStyle.Width(halfWidth).MaxWidth(halfWidth).Render(leftPaneStr)

	// 3. Right pane
	rightLines := make([]string, 0, paneHeight)

	// Render active tab header. Only the current mode is shown so narrow
	// terminals don't wrap the tab list onto a second line.
	tabName := "Preview"
	switch m.rightMode {
	case ModeImpact:
		if m.impactCallees {
			tabName = "Impact (Callees)"
		} else {
			tabName = "Impact (Callers)"
		}
	case ModeFlow:
		tabName = "Flow"
	}

	activeTabStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)
	headerText := activeTabStyle.Render(tabName)
	if m.focus == 1 {
		headerText = glowStyle.Render("● ") + headerText
	}
	rightLines = append(rightLines, headerStyle.Width(rightContentWidth).Render(headerText))
	rightLines = append(rightLines, "")

	if m.rightMode == ModePreview {
		previewLines := m.renderPreview(rightContentWidth, paneHeight-4)
		rightLines = append(rightLines, previewLines...)
	} else if m.rightMode == ModeFlow {
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
			rightLines = append(rightLines, clipStyle.Render(line))
		}
	} else if len(m.rightItems) > 0 {
		for i := 0; i < len(m.rightItems); i++ {
			if len(rightLines) >= paneHeight {
				break
			}
			res := m.rightItems[i]
			n := m.graph.Nodes[res.ID]
			if n != nil {
				filename := filepath.Base(n.Path)
				funcName := n.Name
				line1 := funcStyle.Render("ƒ ") + funcStyle.Render(funcName)
				line2 := "  " + faintStyle.Render(fmt.Sprintf("%s:%d", filename, res.Line))
				sig := getSignature(n.Path, res.Line)
				line3 := ""
				if sig != "" {
					line3 = "  " + faintStyle.Italic(true).Render(sig)
				}
				if i == m.rightSelected && m.focus == 1 {
					padLen := contentWidth - lipgloss.Width(line1)
					if padLen < 0 {
						padLen = 0
					}
					line1 = selectedStyle.Render(line1 + strings.Repeat(" ", padLen))
				}
				rightLines = append(rightLines, clipStyle.Render(line1))
				if len(rightLines) < paneHeight {
					rightLines = append(rightLines, clipStyle.Render(line2))
				}
				if line3 != "" && len(rightLines) < paneHeight {
					rightLines = append(rightLines, clipStyle.Render(line3))
				}
			}
		}
	} else {
		kindStr := "callers"
		if m.impactCallees {
			kindStr = "callees"
		}
		rightLines = append(rightLines, clipStyle.Render("  "+faintStyle.Render(fmt.Sprintf("No %s found", kindStr))))
		rightLines = append(rightLines, clipStyle.Render("  "+faintStyle.Render("Press 'Space' to toggle Callers / Callees")))
	}
	if len(rightLines) > paneHeight {
		rightLines = rightLines[:paneHeight]
	}
	for len(rightLines) < paneHeight {
		rightLines = append(rightLines, "")
	}
	rightPaneStr := lipgloss.JoinVertical(lipgloss.Top, rightLines...)
	rightPane := paneStyle.Width(rightWidth).MaxWidth(rightWidth).Render(rightPaneStr)

	// 4. Bottom Bar
	panes := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)

	// Neovim-style status bar: path │ language │ functions │ mode  [trace info on right]
	sep := faintStyle.Render(" │ ")

	// Left side: project info
	modeName := "Preview"
	switch m.rightMode {
	case ModeImpact:
		if !m.impactCallees {
			modeName = "Callers"
		} else {
			modeName = "Callees"
		}
	case ModeFlow:
		modeName = "Flow"
	}
	statusLeft := " " + textStyle.Render(m.projectPath) + sep +
		lipgloss.NewStyle().Foreground(brightYellow).Render(m.languages)

	if m.width >= 70 {
		statusLeft += sep + faintStyle.Render(fmt.Sprintf("%d functions", m.funcCount))
	}

	statusLeft += sep + lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Render(modeName)

	// Right side: trace status (if active)
	statusRight := ""
	if len(m.history) > 0 {
		if m.isLive {
			statusRight = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true).Render("LIVE")
		} else {
			statusRight = lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true).Render("PAUSED")
		}
		if m.width >= 80 {
			statusRight += " " + faintStyle.Render(fmt.Sprintf("Hit %d/%d", m.playhead+1, len(m.history)))
		}
	}

	var footerStr string
	if m.searching {
		footerStr = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true).Render(" / ") + m.searchText + " "
	} else {
		// Pad between left and right
		gap := m.width - lipgloss.Width(statusLeft) - lipgloss.Width(statusRight) - 1
		if gap < 1 {
			gap = 1
		}
		footerStr = statusLeft + strings.Repeat(" ", gap) + statusRight
	}

	bottomBar := lipgloss.NewStyle().
		Width(m.width).
		Border(lipgloss.NormalBorder(), true, false, false, false).
		BorderForeground(lipgloss.Color("240")).
		Foreground(lipgloss.Color("252")).
		Render(footerStr)

	// 5. Help overlay
	if m.showHelp {
		helpContent := []string{
			headerStyle.Width(40).Render("Keybindings"),
			"",
			"  " + funcStyle.Render("j/k") + faintStyle.Render("         move up/down"),
			"  " + funcStyle.Render("ctrl+j/k") + faintStyle.Render("    jump 5"),
			"  " + funcStyle.Render("h/l") + faintStyle.Render("         collapse/expand"),
			"  " + funcStyle.Render("i") + faintStyle.Render("           toggle focus"),
			"  " + funcStyle.Render("enter") + faintStyle.Render("       open in editor"),
			"  " + funcStyle.Render("t / tab") + faintStyle.Render("     cycle mode (Preview/Impact/Flow)"),
			"  " + funcStyle.Render("space") + faintStyle.Render("       toggle callers/callees or live"),
			"  " + funcStyle.Render("/") + faintStyle.Render("           search"),
			"  " + funcStyle.Render("c") + faintStyle.Render("           collapse/restore"),
			"  " + funcStyle.Render("g g / G") + faintStyle.Render("     top / bottom"),
			"",
			headerStyle.Width(40).Render("Trace Controls"),
			"",
			"  " + funcStyle.Render("H/L") + faintStyle.Render("         scrub history"),
			"  " + funcStyle.Render("f") + faintStyle.Render("           follow mode"),
			"",
			faintStyle.Render("       press ? to close"),
		}
		helpBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(1, 2).
			Background(lipgloss.Color("235")).
			Render(lipgloss.JoinVertical(lipgloss.Left, helpContent...))

		// Center the overlay
		overlay := lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, helpBox)
		return overlay
	}

	return lipgloss.JoinVertical(lipgloss.Top, topBar, panes, bottomBar)
}

func openEditor(item *TreeItem) error {
	var sttyOutput []byte
	if runtime.GOOS != "windows" {
		sttyOutput, _ = exec.Command("stty", "-g").Output()
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		if runtime.GOOS == "windows" {
			editor = "code"
		} else {
			editor = "nvim"
		}
	}
	var cmd *exec.Cmd
	if strings.Contains(editor, "vim") || strings.Contains(editor, "nvim") {
		cmd = exec.Command(editor, fmt.Sprintf("+%d", item.Line), item.Path)
	} else if strings.Contains(editor, "code") || strings.Contains(editor, "cursor") {
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
	if runtime.GOOS != "windows" && len(sttyOutput) > 0 {
		exec.Command("stty", string(sttyOutput)).Run()
	}
	return nil
}

func startEventDispatcher(getProg func() *tea.Program) chan<- tracer.Event {
	events := make(chan tracer.Event, 10000)

	go func() {
		var batch []tracer.Event
		var ticker *time.Ticker
		var tickerChan <-chan time.Time

		flush := func() {
			if len(batch) == 0 {
				return
			}
			prog := getProg()
			if prog != nil {
				if len(batch) == 1 {
					prog.Send(TraceEventMsg(batch[0]))
				} else {
					toSend := make([]tracer.Event, len(batch))
					copy(toSend, batch)
					prog.Send(TraceBatchMsg(toSend))
				}
			}
			batch = nil
			if ticker != nil {
				ticker.Stop()
				ticker = nil
				tickerChan = nil
			}
		}

		for {
			select {
			case e, ok := <-events:
				if !ok {
					flush()
					return
				}
				batch = append(batch, e)
				if ticker == nil {
					ticker = time.NewTicker(33 * time.Millisecond)
					tickerChan = ticker.C
				}

			case <-tickerChan:
				flush()
			}
		}
	}()

	return events
}

func Start(g *graph.Graph, projectRoot string) error {
	m := NewModel(g, projectRoot)

	// Passive Listening: Nav listens on project socket/port
	var prog *tea.Program
	eventChan := startEventDispatcher(func() *tea.Program { return prog })

	listener, _ := tracer.StartListener(projectRoot, func(e tracer.Event) {
		eventChan <- e
	})
	if listener != nil {
		defer listener.Close()
	}

	// Live filesystem watcher for instant diff updates from external editors & AI agents
	fileWatcher, _ := diff.StartWatcher(m.projectRoot, func() {
		if prog != nil {
			prog.Send(DiffUpdatedMsg{})
		}
	})
	if fileWatcher != nil {
		defer fileWatcher.Close()
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
				if m.diffMgr != nil {
					if diffs, err := m.diffMgr.ScanAll(); err == nil {
						m.diffMap = diffs
					}
				}
				m.refreshTree()
				continue
			}
		}
		return nil
	}
}

func StartMonitor(g *graph.Graph, target string, projectRoot string) error {
	m := NewModel(g, projectRoot)
	m.isMonitoring = true
	m.rightMode = ModeFlow

	// Active Listening: Error if socket/port is busy
	var prog *tea.Program
	eventChan := startEventDispatcher(func() *tea.Program { return prog })

	listener, err := tracer.StartListener(projectRoot, func(e tracer.Event) {
		eventChan <- e
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: monitor could not start listener: %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()

	monitorWatcher, _ := diff.StartWatcher(m.projectRoot, func() {
		if prog != nil {
			prog.Send(DiffUpdatedMsg{})
		}
	})
	if monitorWatcher != nil {
		defer monitorWatcher.Close()
	}

	// Optionally start the target
	if target != "" {
		go func() {
			time.Sleep(100 * time.Millisecond)
			tracer.Run(target, func(e tracer.Event) {
				eventChan <- e
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

func formatFileSize(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	} else if b < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(b)/1024.0)
	}
	return fmt.Sprintf("%.1f MB", float64(b)/(1024.0*1024.0))
}

func isCodeFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".py", ".go", ".js", ".jsx", ".ts", ".tsx", ".c", ".h",
		".rs", ".java", ".rb", ".cpp", ".cc", ".cxx", ".hpp",
		".cs", ".php", ".kt", ".swift", ".scala", ".m", ".dart",
		".lua", ".zig", ".sh", ".bash", ".zsh":
		return true
	}
	return false
}

func isBinaryExt(ext string) bool {
	binaryExts := map[string]bool{
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true,
		".svg": true, ".webp": true, ".pdf": true, ".zip": true, ".tar": true,
		".gz": true, ".exe": true, ".bin": true, ".dylib": true, ".so": true,
		".woff": true, ".woff2": true, ".ttf": true, ".eot": true, ".mp4": true,
		".mp3": true, ".lock": true,
	}
	return binaryExts[ext]
}

func (m *Model) updateRightItems() {
	if len(m.items) > 0 {
		selectedItem := m.items[m.selected]
		if selectedItem.Type == graph.FunctionNode && m.rightMode == ModeImpact {
			if !m.impactCallees {
				m.rightItems = analyzer.ImpactAnalysis(m.graph, selectedItem.ID)
			} else {
				m.rightItems = analyzer.TraceAnalysis(m.graph, selectedItem.ID)
			}
		} else {
			m.rightItems = nil
		}
	} else {
		m.rightItems = nil
	}
}

func (m *Model) renderPreview(contentWidth int, maxLines int) []string {
	clipStyle := lipgloss.NewStyle().MaxWidth(contentWidth)
	var lines []string
	if len(m.items) == 0 {
		return []string{"  " + faintStyle.Render("No items in project")}
	}

	item := m.items[m.selected]

	resolvePath := func(p string) string {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if m.projectRoot != "" {
			candidate := filepath.Join(m.projectRoot, p)
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
		candidate := filepath.Join(m.projectPath, p)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		return p
	}

	if item.Type == graph.DirectoryNode {
		if item.HasDiff {
			lines = append(lines, clipStyle.Render("  "+dirStyle.Render("\uf07b "+item.Name)+"  "+diffBadgeStyle.Render("~ modified")+" "+faintStyle.Render("("+item.DiffAge+")")))
			lines = append(lines, clipStyle.Render("  "+faintStyle.Render(item.Path)))
			lines = append(lines, clipStyle.Render("  "+faintStyle.Render("Press 'a' to acknowledge directory | 'A' acknowledge all")))
			lines = append(lines, "")
			lines = append(lines, clipStyle.Render("  "+textStyle.Bold(true).Render("Changed files in this directory:")))

			prefix := filepath.Clean(item.Path)
			if prefix == "." {
				prefix = ""
			} else if !strings.HasSuffix(prefix, "/") {
				prefix += "/"
			}

			count := 0
			for dRel, fd := range m.diffMap {
				if prefix == "" || strings.HasPrefix(dRel, prefix) {
					count++
					fileLine := fmt.Sprintf("  ~ %s (+%d -%d, %s)", dRel, fd.AddedCount, fd.DeletedCount, diff.FormatRelativeTime(fd.ModTime))
					lines = append(lines, clipStyle.Render(diffBadgeStyle.Render(fileLine)))

					for _, node := range m.graph.Nodes {
						if node.Type == graph.FunctionNode && filepath.Clean(node.Path) == dRel {
							if fd.FunctionHasDiff(node.Line, node.EndLine) {
								fnAdded := 0
								fnDeleted := 0
								for _, l := range fd.Lines {
									targetLine := l.NewLine
									if l.Op == diff.OpDelete {
										targetLine = l.DeletedNearLine
									}
									if targetLine >= node.Line && targetLine <= node.EndLine {
										if l.Op == diff.OpInsert {
											fnAdded++
										} else if l.Op == diff.OpDelete {
											fnDeleted++
										}
									}
								}
								fnStats := fmt.Sprintf("(+%d -%d) :%d", fnAdded, fnDeleted, node.Line)
								lines = append(lines, clipStyle.Render("      "+funcStyle.Render("ƒ "+node.Name)+" "+faintStyle.Render(fnStats)))
							}
						}
					}
				}
			}
			if count == 0 {
				lines = append(lines, clipStyle.Render("  "+faintStyle.Render("No active diffs")))
			}
			return lines
		}

		lines = append(lines, clipStyle.Render("  "+dirStyle.Render("\uf07b "+item.Name)))
		lines = append(lines, clipStyle.Render("  "+faintStyle.Render(item.Path)))
		lines = append(lines, "")
		lines = append(lines, clipStyle.Render("  "+faintStyle.Render("Press 'l' or 'Enter' to expand directory")))
		return lines
	}

	if item.Type == graph.FileNode {
		relPath := filepath.Clean(item.Path)
		fd := m.diffMap[relPath]

		if item.HasDiff && fd != nil {
			// DIFF VIEW FOR FILE
			lines = append(lines, clipStyle.Render("  "+textStyle.Bold(true).Render(item.Name)+"  "+diffBadgeStyle.Render("~ modified")+" "+faintStyle.Render(fmt.Sprintf("(+%d -%d, %s)", fd.AddedCount, fd.DeletedCount, item.DiffAge))))
			lines = append(lines, clipStyle.Render("  "+faintStyle.Render(item.Path)+"  "+faintStyle.Render("Press 'a' to acknowledge | 'A' acknowledge all")))
			lines = append(lines, "")

			start := m.previewScroll
			if start < 0 {
				start = 0
			}
			if start >= len(fd.Lines) {
				start = len(fd.Lines) - 1
			}
			if start < 0 {
				start = 0
			}

			for i := start; i < len(fd.Lines) && len(lines) < maxLines; i++ {
				l := fd.Lines[i]
				switch l.Op {
				case diff.OpInsert:
					lineNum := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(fmt.Sprintf("%4d │ + ", l.NewLine))
					content := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(l.Text)
					lines = append(lines, clipStyle.Render(lineNum+content))
				case diff.OpDelete:
					lineNum := lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(fmt.Sprintf("%4s │ - ", "-"))
					content := lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(l.Text)
					lines = append(lines, clipStyle.Render(lineNum+content))
				default:
					lineNum := faintStyle.Render(fmt.Sprintf("%4d │   ", l.NewLine))
					content := textStyle.Render(l.Text)
					lines = append(lines, clipStyle.Render(lineNum+content))
				}
			}
			return lines
		}

		fullPath := resolvePath(item.Path)
		lines = append(lines, clipStyle.Render("  "+textStyle.Bold(true).Render(item.Name)))
		lines = append(lines, clipStyle.Render("  "+faintStyle.Render(item.Path)))
		lines = append(lines, "")

		ext := strings.ToLower(filepath.Ext(item.Path))
		if isBinaryExt(ext) {
			lines = append(lines, clipStyle.Render("  "+faintStyle.Render("Binary file — preview not available")))
			lines = append(lines, clipStyle.Render("  "+faintStyle.Render("Press 'Enter' to open in editor")))
			return lines
		}

		rawBytes, err := os.ReadFile(fullPath)
		if err != nil {
			lines = append(lines, clipStyle.Render("  "+faintStyle.Render("Could not read file: "+err.Error())))
			return lines
		}

		fileLines := strings.Split(string(rawBytes), "\n")
		start := m.previewScroll
		if start < 0 {
			start = 0
		}
		if start >= len(fileLines) {
			start = len(fileLines) - 1
		}
		if start < 0 {
			start = 0
		}

		for i := start; i < len(fileLines) && len(lines) < maxLines; i++ {
			rawLine := strings.TrimRight(fileLines[i], "\r")
			rawLine = strings.ReplaceAll(rawLine, "\t", "    ")
			maxW := contentWidth - 7
			if maxW < 0 {
				maxW = 0
			}
			clipped := lipgloss.NewStyle().MaxWidth(maxW).Render(rawLine)
			lineNum := faintStyle.Render(fmt.Sprintf("%4d │ ", i+1))
			lines = append(lines, lineNum+textStyle.Render(clipped))
		}
		return lines
	}

	if item.Type == graph.FunctionNode {
		n := m.graph.Nodes[item.ID]
		if n == nil {
			return []string{"  " + faintStyle.Render("Function node not found in graph")}
		}

		relPath := filepath.Clean(n.Path)
		fd := m.diffMap[relPath]

		if item.HasDiff && fd != nil {
			var fnDiffLines []diff.Line
			fnAdded := 0
			fnDeleted := 0
			for _, l := range fd.Lines {
				targetLine := l.NewLine
				if l.Op == diff.OpDelete {
					targetLine = l.DeletedNearLine
				}
				if targetLine >= n.Line && targetLine <= n.EndLine {
					fnDiffLines = append(fnDiffLines, l)
					if l.Op == diff.OpInsert {
						fnAdded++
					} else if l.Op == diff.OpDelete {
						fnDeleted++
					}
				}
			}

			// DIFF VIEW FOR FUNCTION
			lines = append(lines, clipStyle.Render("  "+funcStyle.Render("ƒ "+n.Name)+"  "+diffBadgeStyle.Render("~ modified")+" "+faintStyle.Render(fmt.Sprintf("(+%d -%d, %s)", fnAdded, fnDeleted, item.DiffAge))))
			lines = append(lines, clipStyle.Render("  "+faintStyle.Render(fmt.Sprintf("%s:%d", filepath.Base(n.Path), n.Line))+"  "+faintStyle.Render("Press 'a' to acknowledge | 'A' acknowledge all")))
			lines = append(lines, "")

			start := m.previewScroll
			if start < 0 {
				start = 0
			}
			if start >= len(fnDiffLines) {
				start = len(fnDiffLines) - 1
			}
			if start < 0 {
				start = 0
			}

			for i := start; i < len(fnDiffLines) && len(lines) < maxLines; i++ {
				l := fnDiffLines[i]
				switch l.Op {
				case diff.OpInsert:
					lineNum := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(fmt.Sprintf("%4d │ + ", l.NewLine))
					content := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(l.Text)
					lines = append(lines, clipStyle.Render(lineNum+content))
				case diff.OpDelete:
					lineNum := lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(fmt.Sprintf("%4s │ - ", "-"))
					content := lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(l.Text)
					lines = append(lines, clipStyle.Render(lineNum+content))
				default:
					lineNum := faintStyle.Render(fmt.Sprintf("%4d │   ", l.NewLine))
					content := textStyle.Render(l.Text)
					lines = append(lines, clipStyle.Render(lineNum+content))
				}
			}
			return lines
		}

		fullPath := resolvePath(n.Path)
		lines = append(lines, clipStyle.Render("  "+funcStyle.Render("ƒ "+n.Name)+"  "+faintStyle.Render(fmt.Sprintf("%s:%d", filepath.Base(n.Path), n.Line))))
		lines = append(lines, clipStyle.Render("  "+faintStyle.Render(n.Path)))
		lines = append(lines, "")

		rawBytes, err := os.ReadFile(fullPath)
		if err != nil {
			lines = append(lines, clipStyle.Render("  "+faintStyle.Render("Could not read file: "+err.Error())))
			return lines
		}

		fileLines := strings.Split(string(rawBytes), "\n")
		fnStart := n.Line - 1
		fnEnd := n.EndLine
		if fnStart < 0 {
			fnStart = 0
		}
		if fnEnd <= 0 || fnEnd > len(fileLines) {
			fnEnd = len(fileLines)
		}

		start := fnStart + m.previewScroll
		if start < fnStart {
			start = fnStart
		}
		if start >= fnEnd {
			start = fnEnd - 1
		}
		if start < 0 {
			start = 0
		}

		for i := start; i < fnEnd && len(lines) < maxLines; i++ {
			rawLine := strings.TrimRight(fileLines[i], "\r")
			rawLine = strings.ReplaceAll(rawLine, "\t", "    ")
			maxW := contentWidth - 7
			if maxW < 0 {
				maxW = 0
			}
			clipped := lipgloss.NewStyle().MaxWidth(maxW).Render(rawLine)
			lineNum := faintStyle.Render(fmt.Sprintf("%4d │ ", i+1))
			lines = append(lines, lineNum+textStyle.Render(clipped))
		}
		return lines
	}

	return lines
}
