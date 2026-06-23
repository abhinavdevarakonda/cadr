package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"

	"github.com/abhinavdevarakonda/cadr/internal/frameworks"
	"github.com/abhinavdevarakonda/cadr/internal/graph"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	methodStyle = func(m string) lipgloss.Style {
		switch strings.ToUpper(m) {
		case "GET":
			return lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
		case "POST":
			return lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
		case "PUT":
			return lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
		case "DELETE":
			return lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
		default:
			return lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
		}
	}
	headingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)

	activeTabStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("4")).
			Foreground(lipgloss.Color("15")).
			Padding(0, 1).
			Bold(true)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("243")).
				Padding(0, 1)

	// underlined heading style matching main TUI structure headings
	apiHeaderStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")).
			Bold(true).
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(lipgloss.Color("240"))

	searchHighlightStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("11")).
				Foreground(lipgloss.Color("0")).
				Bold(true)
)

func joinPath(prefix, path string) string {
	if prefix == "" {
		return path
	}
	prefix = strings.TrimSuffix(prefix, "/")
	path = strings.TrimPrefix(path, "/")
	return prefix + "/" + path
}

func cleanPathParams(path string) string {
	re := regexp.MustCompile(`<([a-zA-Z_]\w*):([a-zA-Z_]\w*)>`)
	return re.ReplaceAllString(path, "<$2>")
}

// request persistence cache
type SavedRequest struct {
	PathParams  []string `json:"path_params"`
	QueryParams string   `json:"query_params"`
	Headers     string   `json:"headers"`
	Body        string   `json:"body"`
	HitCount    int      `json:"hit_count"`
	LastCalled  string   `json:"last_called"`
}

type APICache map[string]SavedRequest

func loadCache() APICache {
	cache := make(APICache)
	data, err := os.ReadFile(".cadr/api_cache.json")
	if err == nil {
		_ = json.Unmarshal(data, &cache)
	}
	return cache
}

func saveCache(cache APICache) {
	_ = os.MkdirAll(".cadr", 0755)
	data, err := json.MarshalIndent(cache, "", "  ")
	if err == nil {
		_ = os.WriteFile(".cadr/api_cache.json", data, 0644)
	}
}

type APIScreen int

const (
	ListScreen APIScreen = iota
	FormScreen
)

type RightPaneTab int

const (
	TabResponse RightPaneTab = iota
	TabCallGraph
)

type LocationToOpen struct {
	Path string
	Line int
}

type APIConfig struct {
	DefaultURL string
	FlaskURL   string
	FastAPIURL string
}

// TUI model definition
type APIModel struct {
	endpoints   []frameworks.Endpoint
	filtered    []frameworks.Endpoint
	selectedIdx int
	apiConfig   APIConfig
	graph       *graph.Graph

	// ui screen state
	screen               APIScreen
	width                int
	height               int
	searchActive         bool
	searchVal            string
	rightTab             RightPaneTab
	focusRight           bool
	selectedCallGraphIdx int
	responseScrollY      int
	responseSearchActive bool
	responseSearchVal    string
	itemToOpen           *LocationToOpen

	// form & interactive input states
	focusIdx    int
	formEditing bool
	selectedAll bool
	pathInputs  []textinput.Model
	queryInput  textinput.Model
	headerInput textinput.Model
	bodyInput   textarea.Model

	// background http runner states
	loading         bool
	latency         time.Duration
	statusCode      int
	responseBody    string
	responseHeaders http.Header
}

type responseMsg struct {
	statusCode int
	latency    time.Duration
	body       string
	headers    http.Header
	err        error
}

func (m APIModel) Init() tea.Cmd {
	return nil
}

// input management helpers
func (m *APIModel) initForm() {
	ep := m.filtered[m.selectedIdx]
	m.focusIdx = 0
	m.formEditing = false

	cache := loadCache()
	key := ep.Method + ":" + ep.Path
	saved, hasSaved := cache[key]

	halfWidth := m.width / 2
	if halfWidth <= 0 {
		halfWidth = 40
	}

	// dynamic path parameters fields mapping
	m.pathInputs = make([]textinput.Model, len(ep.PathParams))
	for i, p := range ep.PathParams {
		ti := textinput.New()
		ti.Placeholder = fmt.Sprintf("type: %s", p.Type)
		ti.Prompt = fmt.Sprintf("  %s: ", p.Name)
		if hasSaved && i < len(saved.PathParams) {
			ti.SetValue(saved.PathParams[i])
		}
		m.pathInputs[i] = ti
	}

	m.queryInput = textinput.New()
	m.queryInput.Placeholder = "limit=10"
	m.queryInput.Prompt = "  Query params: "
	if hasSaved {
		m.queryInput.SetValue(saved.QueryParams)
	}

	m.headerInput = textinput.New()
	m.headerInput.Placeholder = "Authorization: Bearer x"
	m.headerInput.Prompt = "  Headers: "
	if hasSaved {
		m.headerInput.SetValue(saved.Headers)
	}

	m.bodyInput = textarea.New()
	m.bodyInput.Placeholder = `{"key": "value"}`
	m.bodyInput.SetWidth(halfWidth - 4)
	m.bodyInput.SetHeight(4)
	if hasSaved {
		m.bodyInput.SetValue(saved.Body)
	}

	m.responseBody = ""
	m.statusCode = 0
	m.loading = false
	m.responseScrollY = 0
	m.responseSearchActive = false
	m.responseSearchVal = ""
	m.updateFocus()
}

func (m *APIModel) updateFocus() {
	hasBody := m.methodSupportsBody()
	for i := range m.pathInputs {
		m.pathInputs[i].Blur()
	}
	m.queryInput.Blur()
	m.headerInput.Blur()
	m.bodyInput.Blur()

	if m.formEditing {
		if m.focusIdx < len(m.pathInputs) {
			m.pathInputs[m.focusIdx].Focus()
		} else if m.focusIdx == len(m.pathInputs) {
			m.queryInput.Focus()
		} else if m.focusIdx == len(m.pathInputs)+1 {
			m.headerInput.Focus()
		} else if m.focusIdx == len(m.pathInputs)+2 && hasBody {
			m.bodyInput.Focus()
		}
	}
}

func (m *APIModel) focusInput(idx int) {
	hasBody := m.methodSupportsBody()
	maxIdx := len(m.pathInputs) + 1
	if hasBody {
		maxIdx = len(m.pathInputs) + 2
	}

	if idx < 0 {
		idx = maxIdx
	}
	if idx > maxIdx {
		idx = 0
	}
	m.focusIdx = idx
	m.updateFocus()
}

func (m *APIModel) setSelectedAll(val bool) {
	m.selectedAll = val
	highlightStyle := lipgloss.NewStyle().Background(lipgloss.Color("7")).Foreground(lipgloss.Color("0"))
	defaultStyle := lipgloss.NewStyle()

	if val {
		if m.focusIdx < len(m.pathInputs) {
			m.pathInputs[m.focusIdx].TextStyle = highlightStyle
		} else if m.focusIdx == len(m.pathInputs) {
			m.queryInput.TextStyle = highlightStyle
		} else if m.focusIdx == len(m.pathInputs)+1 {
			m.headerInput.TextStyle = highlightStyle
		} else if m.focusIdx == len(m.pathInputs)+2 && m.methodSupportsBody() {
			m.bodyInput.FocusedStyle.Text = highlightStyle
		}
	} else {
		for i := range m.pathInputs {
			m.pathInputs[i].TextStyle = defaultStyle
		}
		m.queryInput.TextStyle = defaultStyle
		m.headerInput.TextStyle = defaultStyle
		m.bodyInput.FocusedStyle.Text = defaultStyle
	}
}

func (m *APIModel) methodSupportsBody() bool {
	if len(m.filtered) == 0 || m.selectedIdx >= len(m.filtered) {
		return false
	}
	ep := m.filtered[m.selectedIdx]
	method := strings.ToUpper(ep.Method)
	return method == "POST" || method == "PUT" || method == "PATCH"
}

func (m *APIModel) resolveTargetURL(ep frameworks.Endpoint) string {
	if ep.Framework == "flask" {
		return m.apiConfig.FlaskURL
	} else if ep.Framework == "fastapi" {
		return m.apiConfig.FastAPIURL
	}
	return m.apiConfig.DefaultURL
}

func (m *APIModel) getFinalURL() string {
	ep := m.filtered[m.selectedIdx]
	path := ep.Path

	for i, p := range ep.PathParams {
		val := m.pathInputs[i].Value()
		if val == "" {
			val = fmt.Sprintf("<%s>", p.Name)
		}
		rawPlaceholder := fmt.Sprintf("<%s:%s>", p.Type, p.Name)
		path = strings.ReplaceAll(path, rawPlaceholder, val)
		path = strings.ReplaceAll(path, fmt.Sprintf("<%s>", p.Name), val)
	}

	target := m.resolveTargetURL(ep)
	urlStr := joinPath(target, path)
	if m.queryInput.Value() != "" {
		urlStr += "?" + m.queryInput.Value()
	}
	return urlStr
}

func (m *APIModel) applyFilter() {
	if m.searchVal == "" {
		m.filtered = m.endpoints
	} else {
		var filtered []frameworks.Endpoint
		q := strings.ToLower(m.searchVal)
		for _, ep := range m.endpoints {
			if strings.Contains(strings.ToLower(ep.Path), q) || strings.Contains(strings.ToLower(ep.Method), q) {
				filtered = append(filtered, ep)
			}
		}
		m.filtered = filtered
	}
	m.selectedIdx = 0
}

// http request runner
func sendRequest(method, urlStr string, bodyStr string, headersStr string) tea.Cmd {
	return func() tea.Msg {
		start := time.Now()
		var body io.Reader
		if bodyStr != "" {
			body = strings.NewReader(bodyStr)
		}

		req, err := http.NewRequestWithContext(context.Background(), method, urlStr, body)
		if err != nil {
			return responseMsg{err: err}
		}

		req.Header.Set("Content-Type", "application/json")

		if headersStr != "" {
			parts := strings.Split(headersStr, ",")
			for _, part := range parts {
				kv := strings.SplitN(part, ":", 2)
				if len(kv) == 2 {
					req.Header.Set(strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1]))
				}
			}
		}

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return responseMsg{err: err}
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		return responseMsg{
			statusCode: resp.StatusCode,
			latency:    time.Since(start),
			body:       string(respBody),
			headers:    resp.Header,
		}
	}
}

// bubble tea state loop
func (m APIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()

		if key == "ctrl+c" {
			return m, tea.Quit
		}

		if m.focusRight && m.rightTab == TabCallGraph {
			_, nodes := m.getCurrentCallTree()
			if len(nodes) > 0 {
				switch key {
				case "q":
					return m, tea.Quit
				case "j", "down":
					if m.selectedCallGraphIdx < len(nodes)-1 {
						m.selectedCallGraphIdx++
					}
					return m, nil
				case "k", "up":
					if m.selectedCallGraphIdx > 0 {
						m.selectedCallGraphIdx--
					}
					return m, nil
				case "h", "left":
					m.focusRight = false
					return m, nil
				case "t", "tab":
					m.rightTab = TabResponse
					m.focusRight = false
					return m, nil
				case "enter":
					if m.selectedCallGraphIdx >= 0 && m.selectedCallGraphIdx < len(nodes) {
						node := nodes[m.selectedCallGraphIdx]
						m.itemToOpen = &LocationToOpen{
							Path: node.Path,
							Line: node.Line,
						}
						return m, tea.Quit
					}
				}
			}
			return m, nil
		}

		if m.focusRight && m.rightTab == TabResponse {
			if m.responseSearchActive {
				switch key {
				case "enter", "esc":
					m.responseSearchActive = false
					return m, nil
				case "backspace":
					if len(m.responseSearchVal) > 0 {
						m.responseSearchVal = m.responseSearchVal[:len(m.responseSearchVal)-1]
					}
					return m, nil
				default:
					if len(key) == 1 {
						m.responseSearchVal += key
					}
					return m, nil
				}
			}

			switch key {
			case "q":
				return m, tea.Quit
			case "/":
				m.responseSearchActive = true
				m.responseSearchVal = ""
				return m, nil
			case "y":
				_ = clipboard.WriteAll(m.responseBody)
				return m, nil
			case "j", "down":
				halfWidth := m.width / 2
				rightWidth := m.width - halfWidth
				width := rightWidth - 4

				var targetHost string
				if len(m.filtered) > 0 && m.selectedIdx < len(m.filtered) {
					targetHost = m.resolveTargetURL(m.filtered[m.selectedIdx])
				} else {
					targetHost = m.apiConfig.DefaultURL
				}
				title := faintStyle.Render("cadr api") + faintStyle.Render(" | ") + textStyle.Render("host: "+targetHost)
				topBarHeight := lipgloss.Height(lipgloss.NewStyle().Width(m.width).Border(lipgloss.NormalBorder(), false, false, true, false).BorderForeground(lipgloss.Color("240")).Render(title))

				helpBar := faintStyle.Render(" j/k: scroll response · h/left: back to left pane · t/tab: toggle tab · q: quit")
				statusBarHeight := lipgloss.Height(lipgloss.NewStyle().Width(m.width).Border(lipgloss.NormalBorder(), true, false, false, false).BorderForeground(lipgloss.Color("240")).Render(helpBar))

				mainHeight := m.height - topBarHeight - statusBarHeight
				if mainHeight < 0 {
					mainHeight = 0
				}
				contentHeight := mainHeight - 2
				if contentHeight < 0 {
					contentHeight = 0
				}

				maxScroll := m.responseLinesCount(width) - (contentHeight - 2)
				if m.responseScrollY < maxScroll {
					m.responseScrollY++
				}
				return m, nil
			case "k", "up":
				if m.responseScrollY > 0 {
					m.responseScrollY--
				}
				return m, nil
			case "h", "left":
				m.focusRight = false
				return m, nil
			case "t", "tab":
				m.rightTab = TabCallGraph
				m.focusRight = false
				return m, nil
			}
			return m, nil
		}

		if m.screen == ListScreen {
			if m.searchActive {
				switch key {
				case "enter", "esc":
					m.searchActive = false
				case "backspace":
					if len(m.searchVal) > 0 {
						m.searchVal = m.searchVal[:len(m.searchVal)-1]
						m.applyFilter()
					}
				default:
					if len(key) == 1 {
						m.searchVal += key
						m.applyFilter()
					}
				}
				return m, nil
			}

			switch key {
			case "q":
				return m, tea.Quit
			case "j", "down":
				if m.selectedIdx < len(m.filtered)-1 {
					m.selectedIdx++
				}
			case "k", "up":
				if m.selectedIdx > 0 {
					m.selectedIdx--
				}
			case "l", "right":
				if m.rightTab == TabCallGraph {
					_, nodes := m.getCurrentCallTree()
					if len(nodes) > 0 {
						m.focusRight = true
						m.selectedCallGraphIdx = 0
					}
				} else if m.rightTab == TabResponse {
					m.focusRight = true
					m.responseScrollY = 0
				}
			case "t", "tab":
				if m.rightTab == TabResponse {
					m.rightTab = TabCallGraph
				} else {
					m.rightTab = TabResponse
				}
			case "/":
				m.searchActive = true
				m.searchVal = ""
			case "enter":
				if len(m.filtered) > 0 {
					m.screen = FormScreen
					m.initForm()
				}
			case "ctrl+s":
				// run request directly from list using cached configurations
				if len(m.filtered) > 0 {
					ep := m.filtered[m.selectedIdx]
					cache := loadCache()
					key := ep.Method + ":" + ep.Path
					saved, hasSaved := cache[key]
					if !hasSaved {
						saved = SavedRequest{
							PathParams: make([]string, len(ep.PathParams)),
						}
					}

					path := ep.Path
					for i, p := range ep.PathParams {
						val := ""
						if i < len(saved.PathParams) {
							val = saved.PathParams[i]
						}
						if val == "" {
							val = fmt.Sprintf("<%s>", p.Name)
						}
						rawPlaceholder := fmt.Sprintf("<%s:%s>", p.Type, p.Name)
						path = strings.ReplaceAll(path, rawPlaceholder, val)
						path = strings.ReplaceAll(path, fmt.Sprintf("<%s>", p.Name), val)
					}

					target := m.resolveTargetURL(ep)
					urlStr := joinPath(target, path)
					if saved.QueryParams != "" {
						urlStr += "?" + saved.QueryParams
					}

					m.loading = true
					m.rightTab = TabResponse
					m.responseScrollY = 0

					saved.HitCount++
					saved.LastCalled = time.Now().Format(time.RFC3339)
					cache[key] = saved
					saveCache(cache)

					cmd := sendRequest(ep.Method, urlStr, saved.Body, saved.Headers)
					return m, cmd
				}
			}
			return m, nil
		}

		if m.screen == FormScreen {
			if m.formEditing {
				// special key overrides for json body editor
				if m.focusIdx == len(m.pathInputs)+2 && m.methodSupportsBody() {
					if key == "tab" {
						m.bodyInput.InsertString("  ")
						return m, nil
					}
				}

				if key == "ctrl+a" {
					m.setSelectedAll(true)
					return m, nil
				}

				switch key {
				case "esc":
					m.formEditing = false
					m.updateFocus()
					return m, nil
				case "ctrl+s":
					m.loading = true
					urlStr := m.getFinalURL()
					ep := m.filtered[m.selectedIdx]
					bodyVal := ""
					if m.methodSupportsBody() {
						bodyVal = m.bodyInput.Value()
					}

					var pathVals []string
					for _, ti := range m.pathInputs {
						pathVals = append(pathVals, ti.Value())
					}
					cache := loadCache()
					key := ep.Method + ":" + ep.Path
					saved := cache[key]
					saved.PathParams = pathVals
					saved.QueryParams = m.queryInput.Value()
					saved.Headers = m.headerInput.Value()
					saved.Body = bodyVal
					saved.HitCount++
					saved.LastCalled = time.Now().Format(time.RFC3339)
					cache[key] = saved
					saveCache(cache)

					m.rightTab = TabResponse
					m.responseScrollY = 0

					cmd := sendRequest(ep.Method, urlStr, bodyVal, m.headerInput.Value())
					return m, cmd
				}
			} else {
				switch key {
				case "esc", "h", "H", "q":
					m.screen = ListScreen
					return m, nil
				case "l", "right":
					if m.rightTab == TabCallGraph {
						_, nodes := m.getCurrentCallTree()
						if len(nodes) > 0 {
							m.focusRight = true
							m.selectedCallGraphIdx = 0
						}
					} else if m.rightTab == TabResponse {
						m.focusRight = true
						m.responseScrollY = 0
					}
				case "t", "tab":
					if m.rightTab == TabResponse {
						m.rightTab = TabCallGraph
					} else {
						m.rightTab = TabResponse
					}
				case "j", "down":
					m.focusInput(m.focusIdx + 1)
					return m, nil
				case "k", "up":
					m.focusInput(m.focusIdx - 1)
					return m, nil
				case "enter":
					m.formEditing = true
					m.updateFocus()
					return m, nil
				case "ctrl+s":
					m.loading = true
					urlStr := m.getFinalURL()
					ep := m.filtered[m.selectedIdx]
					bodyVal := ""
					if m.methodSupportsBody() {
						bodyVal = m.bodyInput.Value()
					}

					var pathVals []string
					for _, ti := range m.pathInputs {
						pathVals = append(pathVals, ti.Value())
					}
					cache := loadCache()
					key := ep.Method + ":" + ep.Path
					saved := cache[key]
					saved.PathParams = pathVals
					saved.QueryParams = m.queryInput.Value()
					saved.Headers = m.headerInput.Value()
					saved.Body = bodyVal
					saved.HitCount++
					saved.LastCalled = time.Now().Format(time.RFC3339)
					cache[key] = saved
					saveCache(cache)

					m.rightTab = TabResponse
					m.responseScrollY = 0

					cmd := sendRequest(ep.Method, urlStr, bodyVal, m.headerInput.Value())
					return m, cmd
				}
			}
		}

	case responseMsg:
		m.loading = false
		if msg.err != nil {
			m.statusCode = 500
			ep := m.filtered[m.selectedIdx]
			target := m.resolveTargetURL(ep)
			m.responseBody = fmt.Sprintf("Error: %v\n\nIs your API server running at %s?", msg.err, target)
			m.responseHeaders = nil
		} else {
			m.statusCode = msg.statusCode
			m.latency = msg.latency
			m.responseBody = m.prettifyJSON(msg.body)
			m.responseHeaders = msg.headers
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		halfWidth := m.width / 2
		if m.screen == FormScreen {
			m.bodyInput.SetWidth(halfWidth - 4)
		}
	}

	// route events to active text inputs
	if m.screen == FormScreen && m.formEditing {
		var cmd tea.Cmd

		if m.selectedAll {
			if msgKey, ok := msg.(tea.KeyMsg); ok {
				k := msgKey.String()
				if k != "ctrl+a" && k != "esc" && k != "ctrl+s" {
					isNavigation := k == "left" || k == "right" || k == "up" || k == "down" || k == "home" || k == "end"
					if !isNavigation {
						if m.focusIdx < len(m.pathInputs) {
							m.pathInputs[m.focusIdx].SetValue("")
						} else if m.focusIdx == len(m.pathInputs) {
							m.queryInput.SetValue("")
						} else if m.focusIdx == len(m.pathInputs)+1 {
							m.headerInput.SetValue("")
						} else if m.focusIdx == len(m.pathInputs)+2 && m.methodSupportsBody() {
							m.bodyInput.SetValue("")
						}
					}
					m.setSelectedAll(false)
				}
			}
		}

		if m.focusIdx < len(m.pathInputs) {
			m.pathInputs[m.focusIdx], cmd = m.pathInputs[m.focusIdx].Update(msg)
			cmds = append(cmds, cmd)
		} else if m.focusIdx == len(m.pathInputs) {
			m.queryInput, cmd = m.queryInput.Update(msg)
			cmds = append(cmds, cmd)
		} else if m.focusIdx == len(m.pathInputs)+1 {
			m.headerInput, cmd = m.headerInput.Update(msg)
			cmds = append(cmds, cmd)
		} else if m.focusIdx == len(m.pathInputs)+2 && m.methodSupportsBody() {
			m.bodyInput, cmd = m.bodyInput.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m APIModel) prettifyJSON(body string) string {
	var obj interface{}
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		return body
	}
	pretty, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return body
	}
	return string(pretty)
}

// layout rendering
func (m APIModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing cadr api..."
	}

	// 1. top bar (shows target host for currently selected endpoint)
	var targetHost string
	if len(m.filtered) > 0 && m.selectedIdx < len(m.filtered) {
		targetHost = m.resolveTargetURL(m.filtered[m.selectedIdx])
	} else {
		targetHost = m.apiConfig.DefaultURL
	}
	title := faintStyle.Render("cadr api") + faintStyle.Render(" | ") + textStyle.Render("host: "+targetHost)
	topBar := lipgloss.NewStyle().
		Width(m.width).
		Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(lipgloss.Color("240")).
		Render(title)

	var helpBar string
	if m.focusRight {
		if m.rightTab == TabResponse {
			if m.responseSearchActive {
				helpBar = faintStyle.Render(" esc/enter: exit search · type to search/filter in response")
			} else {
				helpBar = faintStyle.Render(" j/k: scroll response · /: search · y: copy body · h/left: back to left pane · t/tab: toggle tab · q: quit")
			}
		} else {
			helpBar = faintStyle.Render(" j/k: scroll graph · enter: open file in Neovim · h/left: back to left pane · t/tab: toggle tab · q: quit")
		}
	} else if m.screen == ListScreen {
		helpBar = faintStyle.Render(" j/k: navigate · enter: request builder · ctrl+s: execute · t/tab: toggle tab · q: quit")
	} else {
		if m.formEditing {
			helpBar = faintStyle.Render(" esc: stop editing · ctrl+s: execute request")
		} else {
			helpBar = faintStyle.Render(" q/H: back to list · j/k: navigate fields · enter: edit field · t/tab: toggle tab · ctrl+s: execute request")
		}
	}
	statusBar := lipgloss.NewStyle().
		Width(m.width).
		Border(lipgloss.NormalBorder(), true, false, false, false).
		BorderForeground(lipgloss.Color("240")).
		Render(helpBar)

	mainHeight := m.height - lipgloss.Height(topBar) - lipgloss.Height(statusBar)
	if mainHeight < 0 {
		mainHeight = 0
	}
	contentHeight := mainHeight - 2 // account for top/bottom padding of 1
	if contentHeight < 0 {
		contentHeight = 0
	}

	halfWidth := m.width / 2
	leftWidth := halfWidth
	rightWidth := m.width - halfWidth

	var leftView string
	if m.screen == ListScreen {
		leftView = m.leftListView(contentHeight, leftWidth-4)
	} else {
		leftView = m.leftFormView(contentHeight, leftWidth-4)
	}

	var rightView string
	if m.rightTab == TabCallGraph {
		rightView = m.rightCallGraphView(contentHeight, rightWidth-4)
	} else {
		rightView = m.rightResponseView(contentHeight, rightWidth-4)
	}

	leftPane := paneStyle.Width(leftWidth).Render(leftView)
	rightPane := paneStyle.Width(rightWidth).Render(rightView)

	mainView := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)

	return lipgloss.JoinVertical(lipgloss.Left, topBar, mainView, statusBar)
}

func (m APIModel) leftListView(height int, width int) string {
	var lines []string
	header := "Discovered Endpoints"
	if m.searchActive {
		header = fmt.Sprintf("Search: %s_", m.searchVal)
	}
	lines = append(lines, apiHeaderStyle.Width(width).Render(header))
	lines = append(lines, "")

	if len(m.filtered) == 0 {
		lines = append(lines, faintStyle.Render("  No endpoints found."))
	} else {
		// each endpoint block takes exactly 4 lines (method, path, stats, space)
		itemsPerPage := (height - 2) / 4
		if itemsPerPage <= 0 {
			itemsPerPage = 1
		}

		start := 0
		if len(m.filtered) > itemsPerPage {
			if m.selectedIdx >= itemsPerPage/2 {
				start = m.selectedIdx - itemsPerPage/2
			}
			if start+itemsPerPage > len(m.filtered) {
				start = len(m.filtered) - itemsPerPage
			}
			if start < 0 {
				start = 0
			}
		}

		for i := start; i < len(m.filtered) && i < start+itemsPerPage; i++ {
			ep := m.filtered[i]
			methodPrefix := "  "
			if !m.focusRight && i == m.selectedIdx {
				methodPrefix = "> "
			}

			method := methodStyle(ep.Method).Render(ep.Method)
			path := cleanPathParams(ep.Path)

			pathStr := path
			if len(pathStr) > width-2 {
				pathStr = pathStr[:width-5] + "…"
			}
			if !m.focusRight && i == m.selectedIdx {
				pathStr = selectedStyle.Render(pathStr)
			}

			// get cached statistics
			cache := loadCache()
			key := ep.Method + ":" + ep.Path
			saved, hasSaved := cache[key]
			stats := ""
			if hasSaved && saved.HitCount > 0 {
				timeStr := "-"
				if saved.LastCalled != "" {
					t, err := time.Parse(time.RFC3339, saved.LastCalled)
					if err == nil {
						timeStr = formatRelativeTime(t)
					}
				}
				stats = fmt.Sprintf("  • %d hits • %s", saved.HitCount, timeStr)
			}

			lines = append(lines, fmt.Sprintf("%s%s", methodPrefix, method))
			lines = append(lines, fmt.Sprintf("  %s", pathStr))
			if stats != "" {
				if len(stats) > width-2 {
					stats = stats[:width-5] + "…"
				}
				lines = append(lines, faintStyle.Render("  "+stats))
			} else {
				lines = append(lines, "")
			}
			lines = append(lines, "")
		}
	}

	var flatLines []string
	for _, l := range lines {
		flatLines = append(flatLines, strings.Split(l, "\n")...)
	}

	for len(flatLines) < height {
		flatLines = append(flatLines, "")
	}
	return lipgloss.JoinVertical(lipgloss.Left, flatLines[:height]...)
}

func (m APIModel) leftFormView(height int, width int) string {
	var reqLines []string

	reqTitle := apiHeaderStyle.Width(width).Render("Request Builder")
	reqLines = append(reqLines, reqTitle, "")

	ep := m.filtered[m.selectedIdx]

	if len(m.pathInputs) > 0 {
		for i, ti := range m.pathInputs {
			p := ep.PathParams[i]
			prefix := "  "
			headerStyle := headingStyle
			if !m.focusRight && i == m.focusIdx {
				prefix = "> "
				headerStyle = selectedStyle
			}
			ti.Prompt = "  "
			reqLines = append(reqLines, headerStyle.Render(fmt.Sprintf("%s%s:", prefix, p.Name)))
			reqLines = append(reqLines, ti.View())
			reqLines = append(reqLines, "")
		}
	}

	queryPrefix := "  "
	queryHeaderStyle := headingStyle
	if !m.focusRight && m.focusIdx == len(m.pathInputs) {
		queryPrefix = "> "
		queryHeaderStyle = selectedStyle
	}
	m.queryInput.Prompt = "  "
	reqLines = append(reqLines, queryHeaderStyle.Render(fmt.Sprintf("%sQuery params:", queryPrefix)))
	reqLines = append(reqLines, m.queryInput.View())
	reqLines = append(reqLines, "")

	headerPrefix := "  "
	headerHeaderStyle := headingStyle
	if !m.focusRight && m.focusIdx == len(m.pathInputs)+1 {
		headerPrefix = "> "
		headerHeaderStyle = selectedStyle
	}
	m.headerInput.Prompt = "  "
	reqLines = append(reqLines, headerHeaderStyle.Render(fmt.Sprintf("%sHeaders:", headerPrefix)))
	reqLines = append(reqLines, m.headerInput.View())
	reqLines = append(reqLines, "")

	if m.methodSupportsBody() {
		bodyPrefix := "  "
		bodyHeaderStyle := headingStyle
		bodyText := "Body (JSON):"
		if !m.focusRight && m.focusIdx == len(m.pathInputs)+2 {
			bodyPrefix = "> "
			bodyHeaderStyle = selectedStyle
			if m.formEditing {
				bodyText = "Body (JSON) (Editing):"
			} else {
				bodyText = "Body (JSON) (Enter to edit):"
			}
		}
		reqLines = append(reqLines, bodyHeaderStyle.Render(fmt.Sprintf("%s%s", bodyPrefix, bodyText)))
		reqLines = append(reqLines, m.bodyInput.View())
	}

	var flatLines []string
	for _, l := range reqLines {
		flatLines = append(flatLines, strings.Split(l, "\n")...)
	}

	for len(flatLines) < height {
		flatLines = append(flatLines, "")
	}
	return lipgloss.JoinVertical(lipgloss.Left, flatLines[:height]...)
}

func wrapLine(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}
	if len(line) <= width {
		return []string{line}
	}
	var wrapped []string
	for len(line) > width {
		breakIdx := width
		for j := width - 1; j > width-15 && j > 0; j-- {
			if line[j] == ' ' {
				breakIdx = j
				break
			}
		}
		wrapped = append(wrapped, line[:breakIdx])
		line = line[breakIdx:]
		if len(line) > 0 && line[0] == ' ' {
			line = line[1:]
		}
	}
	if len(line) > 0 {
		wrapped = append(wrapped, line)
	}
	return wrapped
}

func (m APIModel) responseLinesCount(width int) int {
	if m.statusCode <= 0 {
		return 0
	}
	count := 6 // status, latency, empty, [Headers], empty, [Body]
	for k, v := range m.responseHeaders {
		line := fmt.Sprintf("%s: %s", k, strings.Join(v, ", "))
		count += len(wrapLine(line, width-6))
	}
	bodyLines := strings.Split(m.responseBody, "\n")
	for _, line := range bodyLines {
		count += len(wrapLine(line, width-4))
	}
	return count
}

func (m APIModel) rightResponseView(height int, width int) string {
	var respLines []string

	var respTitle string
	if m.responseSearchActive {
		respTitle = apiHeaderStyle.Width(width).Render(fmt.Sprintf("Response (Search: %s_)", m.responseSearchVal))
	} else {
		respTitle = apiHeaderStyle.Width(width).Render("Response")
	}
	respLines = append(respLines, respTitle, "")

	if m.loading {
		respLines = append(respLines, faintStyle.Render("  Sending request..."))
	} else if m.statusCode > 0 {
		statusColor := "2"
		if m.statusCode >= 400 {
			statusColor = "9"
		} else if m.statusCode >= 300 {
			statusColor = "3"
		}
		statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(statusColor)).Bold(true)

		respLines = append(respLines,
			fmt.Sprintf("  Status:  %s", statusStyle.Render(fmt.Sprintf("%d", m.statusCode))),
			fmt.Sprintf("  Latency: %v", m.latency),
			"",
			headingStyle.Render("  [Headers]"),
		)
		var keys []string
		for k := range m.responseHeaders {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := m.responseHeaders[k]
			line := fmt.Sprintf("%s: %s", k, strings.Join(v, ", "))
			wrapped := wrapLine(line, width-6)
			for _, wl := range wrapped {
				highlighted := wl
				if m.responseSearchVal != "" {
					highlighted = highlightMatches(wl, m.responseSearchVal)
				}
				respLines = append(respLines, "    "+highlighted)
			}
		}
		respLines = append(respLines, "", headingStyle.Render("  [Body]"))

		bodyLines := strings.Split(strings.ReplaceAll(m.responseBody, "\r", ""), "\n")
		for _, line := range bodyLines {
			wrapped := wrapLine(line, width-4)
			for _, wl := range wrapped {
				highlighted := wl
				if m.responseSearchVal != "" {
					highlighted = highlightMatches(wl, m.responseSearchVal)
				}
				respLines = append(respLines, "    "+highlighted)
			}
		}
	} else {
		wrapped := wrapLine("ctrl+s to send a request.", width-4)
		for _, wl := range wrapped {
			respLines = append(respLines, faintStyle.Render("  "+wl))
		}
	}

	var flatRespLines []string
	for _, l := range respLines {
		flatRespLines = append(flatRespLines, strings.Split(l, "\n")...)
	}

	titleCount := 3
	contentLines := flatRespLines[titleCount:]

	contentHeight := height - titleCount
	if contentHeight < 0 {
		contentHeight = 0
	}

	scrollY := m.responseScrollY
	if scrollY < 0 {
		scrollY = 0
	}
	if scrollY > len(contentLines)-contentHeight {
		scrollY = len(contentLines) - contentHeight
	}
	if scrollY < 0 {
		scrollY = 0
	}

	var visibleLines []string
	visibleLines = append(visibleLines, flatRespLines[:titleCount]...)

	endIdx := scrollY + contentHeight
	if endIdx > len(contentLines) {
		endIdx = len(contentLines)
	}
	visibleLines = append(visibleLines, contentLines[scrollY:endIdx]...)

	for len(visibleLines) < height {
		visibleLines = append(visibleLines, "")
	}
	return lipgloss.JoinVertical(lipgloss.Left, visibleLines[:height]...)
}

func highlightMatches(text string, query string) string {
	if query == "" {
		return text
	}
	lowerText := strings.ToLower(text)
	lowerQuery := strings.ToLower(query)

	var result strings.Builder
	start := 0
	for {
		idx := strings.Index(lowerText[start:], lowerQuery)
		if idx == -1 {
			break
		}
		matchIdx := start + idx
		result.WriteString(text[start:matchIdx])
		match := text[matchIdx : matchIdx+len(query)]
		result.WriteString(searchHighlightStyle.Render(match))
		start = matchIdx + len(query)
	}
	result.WriteString(text[start:])
	return result.String()
}

func (m APIModel) rightCallGraphView(height int, width int) string {
	var renderLines []string

	title := apiHeaderStyle.Width(width).Render("Static Call Graph")
	renderLines = append(renderLines, title, "")

	lines, nodes := m.getCurrentCallTree()
	if len(nodes) == 0 {
		for _, l := range lines {
			wrapped := wrapLine(l, width-4)
			for _, wl := range wrapped {
				renderLines = append(renderLines, faintStyle.Render("  "+wl))
			}
		}
	} else {
		// cap selection idx
		selIdx := m.selectedCallGraphIdx
		if selIdx < 0 {
			selIdx = 0
		}
		if selIdx >= len(nodes) {
			selIdx = len(nodes) - 1
		}

		// calculate nodes to fit in the screen viewport
		// each node takes exactly 2 lines (line 1: function name, line 2: file and line)
		nodesPerPage := (height - 2) / 2
		if nodesPerPage <= 0 {
			nodesPerPage = 1
		}

		start := 0
		if len(nodes) > nodesPerPage {
			if selIdx >= nodesPerPage/2 {
				start = selIdx - nodesPerPage/2
			}
			if start+nodesPerPage > len(nodes) {
				start = len(nodes) - nodesPerPage
			}
			if start < 0 {
				start = 0
			}
		}

		for i := start; i < len(nodes) && i < start+nodesPerPage; i++ {
			node := nodes[i]
			selPrefix := "  "
			if m.focusRight && i == selIdx {
				selPrefix = "> "
			}

			indent := strings.Repeat("  ", node.Depth)
			treePrefix := "├─ "
			if node.Depth == 0 {
				treePrefix = ""
			}

			nameStr := node.Name
			if len(nameStr) > width-4-node.Depth*2 {
				nameStr = nameStr[:width-7-node.Depth*2] + "…"
			}
			var nameRendered string
			if m.focusRight && i == selIdx {
				nameRendered = selectedStyle.Render(nameStr)
			} else {
				nameRendered = funcStyle.Render(nameStr)
			}
			line1 := fmt.Sprintf("%s%s%s%s", selPrefix, indent, treePrefix, nameRendered)

			// render line 2: file and line (faint grey on next line, matching main TUI.go)
			shortPath := filepath.Base(node.Path)
			fileLine := fmt.Sprintf("%s:%d", shortPath, node.Line)
			if len(fileLine) > width-6-node.Depth*2 {
				fileLine = fileLine[:width-9-node.Depth*2] + "…"
			}
			line2 := fmt.Sprintf("  %s  %s", indent, fileLine)
			line2 = faintStyle.Render(line2)

			renderLines = append(renderLines, line1, line2)
		}
	}

	var flatLines []string
	for _, l := range renderLines {
		flatLines = append(flatLines, strings.Split(l, "\n")...)
	}

	for len(flatLines) < height {
		flatLines = append(flatLines, "")
	}
	return lipgloss.JoinVertical(lipgloss.Left, flatLines[:height]...)
}

// call graph tree construction and finding functions
func (m *APIModel) getCurrentCallTree() ([]string, []*CallTreeNode) {
	if len(m.filtered) == 0 || m.selectedIdx >= len(m.filtered) {
		return []string{"No endpoint selected"}, nil
	}
	ep := m.filtered[m.selectedIdx]
	handlerNode := findFunctionNode(m.graph, ep)
	if handlerNode == nil {
		return []string{fmt.Sprintf("Could not find handler function '%s' in graph.", ep.HandlerFunc)}, nil
	}

	visited := make(map[string]bool)
	tree := buildCallTree(m.graph, handlerNode.ID, 0, 5, visited)
	if tree == nil {
		return []string{"Empty call graph"}, nil
	}

	var lines []string
	var nodes []*CallTreeNode
	currIdx := 0
	dummySel := -1
	flattenCallTree(tree, &lines, &dummySel, -1, &currIdx, &nodes)
	return lines, nodes
}

type CallTreeNode struct {
	Name     string
	Path     string
	Line     int
	Depth    int
	Children []*CallTreeNode
}

func findFunctionNode(g *graph.Graph, ep frameworks.Endpoint) *graph.Node {
	if g == nil {
		return nil
	}
	var best *graph.Node
	bestDist := 999999

	for _, n := range g.Nodes {
		if n.Type != graph.FunctionNode {
			continue
		}
		// match file name and function name
		if strings.HasSuffix(n.Path, ep.File) || strings.HasSuffix(ep.File, n.Path) {
			if n.Name == ep.HandlerFunc {
				dist := n.Line - ep.Line
				if dist >= 0 && dist < bestDist {
					best = n
					bestDist = dist
				}
			}
		}
	}
	return best
}

func buildCallTree(g *graph.Graph, nodeID string, depth int, maxDepth int, visited map[string]bool) *CallTreeNode {
	node, exists := g.Nodes[nodeID]
	if !exists {
		return nil
	}

	tn := &CallTreeNode{
		Name:  node.Name,
		Path:  node.Path,
		Line:  node.Line,
		Depth: depth,
	}

	if depth >= maxDepth || visited[nodeID] {
		return tn
	}

	visited[nodeID] = true
	defer func() { visited[nodeID] = false }()

	for _, childID := range g.AdjOut[nodeID] {
		childTN := buildCallTree(g, childID, depth+1, maxDepth, visited)
		if childTN != nil {
			tn.Children = append(tn.Children, childTN)
		}
	}

	return tn
}

func flattenCallTree(tn *CallTreeNode, lines *[]string, selectedLine *int, targetIdx int, currentIdx *int, nodesList *[]*CallTreeNode) {
	if tn == nil {
		return
	}

	indent := strings.Repeat("  ", tn.Depth)
	prefix := "├─ "
	if tn.Depth == 0 {
		prefix = ""
	}

	shortPath := filepath.Base(tn.Path)
	lineStr := fmt.Sprintf("%s%s%s (%s:%d)", indent, prefix, tn.Name, shortPath, tn.Line)

	if *currentIdx == targetIdx {
		*selectedLine = len(*lines)
	}

	*lines = append(*lines, lineStr)
	*nodesList = append(*nodesList, tn)
	*currentIdx++

	for _, child := range tn.Children {
		flattenCallTree(child, lines, selectedLine, targetIdx, currentIdx, nodesList)
	}
}

// relative time formatting
func formatRelativeTime(t time.Time) string {
	diff := time.Since(t)
	if diff < time.Second {
		return "just now"
	}
	if diff < time.Minute {
		return fmt.Sprintf("%ds ago", int(diff.Seconds()))
	}
	if diff < time.Hour {
		return fmt.Sprintf("%dm ago", int(diff.Minutes()))
	}
	if diff < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(diff.Hours()))
	}
	return t.Format("2006-01-02")
}

// neovim editor integration
func openAPIEditor(loc *LocationToOpen) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
		if editor == "" {
			editor = "nvim"
		}
	}

	var sttyOutput []byte
	if runtime.GOOS != "windows" {
		sttyOutput, _ = exec.Command("stty", "-g").Output()
	}

	var cmd *exec.Cmd
	if strings.Contains(editor, "vim") || strings.Contains(editor, "nvim") {
		cmd = exec.Command(editor, fmt.Sprintf("+%d", loc.Line), loc.Path)
	} else if strings.Contains(editor, "code") || strings.Contains(editor, "cursor") {
		cmd = exec.Command(editor, "-g", fmt.Sprintf("%s:%d", loc.Path, loc.Line))
	} else {
		cmd = exec.Command(editor, loc.Path)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()

	if runtime.GOOS != "windows" && len(sttyOutput) > 0 {
		exec.Command("stty", string(sttyOutput)).Run()
	}
}

// entry point
func StartAPI(endpoints []frameworks.Endpoint, config APIConfig, g *graph.Graph) error {
	model := APIModel{
		endpoints: endpoints,
		filtered:  endpoints,
		apiConfig: config,
		screen:    ListScreen,
		graph:     g,
		rightTab:  TabCallGraph,
	}
	model.bodyInput = textarea.New()

	for {
		p := tea.NewProgram(&model, tea.WithAltScreen())
		finalModel, err := p.Run()
		if err != nil {
			return err
		}
		if returnedModel, ok := finalModel.(APIModel); ok {
			model = returnedModel
			if model.itemToOpen != nil {
				openAPIEditor(model.itemToOpen)
				model.itemToOpen = nil
				continue
			}
		}
		return nil
	}
}
