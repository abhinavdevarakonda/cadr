package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/abhinavdevarakonda/cadr/internal/frameworks"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// color palette

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
	headingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true) // terminal adaptive blue
)

func joinPath(prefix, path string) string {
	if prefix == "" {
		return path
	}
	prefix = strings.TrimSuffix(prefix, "/")
	path = strings.TrimPrefix(path, "/")
	return prefix + "/" + path
}

// Request Persistence Cache
type SavedRequest struct {
	PathParams  []string `json:"path_params"`
	QueryParams string   `json:"query_params"`
	Headers     string   `json:"headers"`
	Body        string   `json:"body"`
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

// TUI model definition

type APIModel struct {
	endpoints   []frameworks.Endpoint
	filtered    []frameworks.Endpoint
	selectedIdx int
	targetURL   string

	// UI screen state
	screen       APIScreen
	width        int
	height       int
	searchActive bool
	searchVal    string

	// form & interactive input states
	focusIdx    int
	formEditing bool
	selectedAll bool
	pathInputs  []textinput.Model
	queryInput  textinput.Model
	headerInput textinput.Model
	bodyInput   textarea.Model

	// background HTTP runner states
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

	// dynamic path parameters fields mapping
	m.pathInputs = make([]textinput.Model, len(ep.PathParams))
	for i, p := range ep.PathParams {
		ti := textinput.New()
		ti.Placeholder = fmt.Sprintf("e.g. 42 (type: %s)", p.Type)
		ti.Prompt = fmt.Sprintf("  %s: ", p.Name)
		if hasSaved && i < len(saved.PathParams) {
			ti.SetValue(saved.PathParams[i])
		}
		m.pathInputs[i] = ti
	}

	m.queryInput = textinput.New()
	m.queryInput.Placeholder = "e.g. limit=10"
	m.queryInput.Prompt = "  Query params: "
	if hasSaved {
		m.queryInput.SetValue(saved.QueryParams)
	}

	m.headerInput = textinput.New()
	m.headerInput.Placeholder = "e.g. Authorization: Bearer x, X-Custom: y"
	m.headerInput.Prompt = "  Headers: "
	if hasSaved {
		m.headerInput.SetValue(saved.Headers)
	}

	m.bodyInput = textarea.New()
	m.bodyInput.Placeholder = "{\n  \"key\": \"value\"\n}"
	m.bodyInput.SetWidth(50)
	m.bodyInput.SetHeight(6)
	if hasSaved {
		m.bodyInput.SetValue(saved.Body)
	}

	m.responseBody = ""
	m.statusCode = 0
	m.loading = false
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
	ep := m.filtered[m.selectedIdx]
	method := strings.ToUpper(ep.Method)
	return method == "POST" || method == "PUT" || method == "PATCH"
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

	urlStr := joinPath(m.targetURL, path)
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

// HTTP request runner

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
			case "/":
				m.searchActive = true
				m.searchVal = ""
			case "enter":
				if len(m.filtered) > 0 {
					m.screen = FormScreen
					m.initForm()
				}
			}
			return m, nil
		}

		if m.screen == FormScreen {
			if m.formEditing {
				// Special key overrides for JSON body editor
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

					// Persist parameters to cache
					var pathVals []string
					for _, ti := range m.pathInputs {
						pathVals = append(pathVals, ti.Value())
					}
					cache := loadCache()
					cache[ep.Method+":"+ep.Path] = SavedRequest{
						PathParams:  pathVals,
						QueryParams: m.queryInput.Value(),
						Headers:     m.headerInput.Value(),
						Body:        bodyVal,
					}
					saveCache(cache)

					cmd := sendRequest(ep.Method, urlStr, bodyVal, m.headerInput.Value())
					return m, cmd
				}
			} else {
				switch key {
				case "esc", "h", "H":
					m.screen = ListScreen
					return m, nil
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

					// Persist parameters to cache
					var pathVals []string
					for _, ti := range m.pathInputs {
						pathVals = append(pathVals, ti.Value())
					}
					cache := loadCache()
					cache[ep.Method+":"+ep.Path] = SavedRequest{
						PathParams:  pathVals,
						QueryParams: m.queryInput.Value(),
						Headers:     m.headerInput.Value(),
						Body:        bodyVal,
					}
					saveCache(cache)

					cmd := sendRequest(ep.Method, urlStr, bodyVal, m.headerInput.Value())
					return m, cmd
				}
			}
		}

	case responseMsg:
		m.loading = false
		if msg.err != nil {
			m.statusCode = 500
			m.responseBody = fmt.Sprintf("Error: %v\n\nIs your API server running at %s?", msg.err, m.targetURL)
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
	if m.width == 0 {
		return "Initializing cadr api..."
	}

	// 1. top bar
	title := faintStyle.Render("cadr api") + faintStyle.Render(" | ") + textStyle.Render("host: "+m.targetURL)
	topBar := lipgloss.NewStyle().
		Width(m.width).
		Border(lipgloss.NormalBorder(), false, false, true, false).
		BorderForeground(lipgloss.Color("240")).
		Render(title)

	// 2. bottom status bar
	var helpBar string
	if m.screen == ListScreen {
		helpBar = faintStyle.Render(" j/k: navigate · enter: open · /: search · q: quit")
	} else {
		if m.formEditing {
			helpBar = faintStyle.Render(" esc: stop editing · ctrl+s: execute request")
		} else {
			helpBar = faintStyle.Render(" esc: back to list · j/k: navigate fields · enter: edit field · ctrl+s: execute request")
		}
	}
	statusBar := lipgloss.NewStyle().
		Width(m.width).
		Border(lipgloss.NormalBorder(), true, false, false, false).
		BorderForeground(lipgloss.Color("240")).
		Render(helpBar)

	mainHeight := m.height - lipgloss.Height(topBar) - lipgloss.Height(statusBar) - 2
	if mainHeight < 0 {
		mainHeight = 0
	}

	var mainView string
	if m.screen == ListScreen {
		mainView = m.listView(mainHeight)
	} else {
		mainView = m.formView(mainHeight)
	}

	return lipgloss.JoinVertical(lipgloss.Left, topBar, mainView, statusBar)
}

func (m APIModel) listView(height int) string {
	var lines []string
	header := "Discovered Endpoints"
	if m.searchActive {
		header = fmt.Sprintf("Search: %s_", m.searchVal)
	}
	lines = append(lines, headerStyle.Width(m.width-4).Render(header))
	lines = append(lines, "")

	if len(m.filtered) == 0 {
		lines = append(lines, "  No endpoints found.")
	} else {
		for i, ep := range m.filtered {
			style := textStyle
			prefix := "  "
			if i == m.selectedIdx {
				style = selectedStyle
				prefix = "> "
			}

			method := methodStyle(ep.Method).Render(fmt.Sprintf("%-6s", ep.Method))
			path := ep.Path
			lines = append(lines, style.Render(fmt.Sprintf("%s%s %s", prefix, method, path)))
		}
	}

	for len(lines) < height {
		lines = append(lines, "")
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines[:height]...)
}

func (m APIModel) formView(height int) string {
	halfWidth := m.width / 2

	// left pane: input form
	var reqLines []string
	ep := m.filtered[m.selectedIdx]

	reqTitle := headerStyle.Width(halfWidth - 4).Render(fmt.Sprintf("Request: %s %s", ep.Method, ep.Path))
	reqLines = append(reqLines, reqTitle, "")

	if len(m.pathInputs) > 0 {
		reqLines = append(reqLines, headingStyle.Render("  [Path Parameters]"))
		for i, ti := range m.pathInputs {
			p := ep.PathParams[i]
			prefix := "  "
			promptStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
			if i == m.focusIdx {
				prefix = "❯ "
				promptStyle = selectedStyle
			}
			ti.Prompt = prefix + promptStyle.Render(p.Name+": ")
			reqLines = append(reqLines, ti.View())
		}
		reqLines = append(reqLines, "")
	}

	queryPrefix := "  "
	queryPromptStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	if m.focusIdx == len(m.pathInputs) {
		queryPrefix = "❯ "
		queryPromptStyle = selectedStyle
	}
	m.queryInput.Prompt = queryPrefix + queryPromptStyle.Render("Query params: ")
	reqLines = append(reqLines, headingStyle.Render("  [Query Parameters]"), m.queryInput.View(), "")

	headerPrefix := "  "
	headerPromptStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	if m.focusIdx == len(m.pathInputs)+1 {
		headerPrefix = "❯ "
		headerPromptStyle = selectedStyle
	}
	m.headerInput.Prompt = headerPrefix + headerPromptStyle.Render("Headers: ")
	reqLines = append(reqLines, headingStyle.Render("  [Headers]"), m.headerInput.View(), "")

	if m.methodSupportsBody() {
		bodyHeader := "  [JSON Body]"
		if m.focusIdx == len(m.pathInputs)+2 {
			if m.formEditing {
				bodyHeader = "❯ [JSON Body] (Editing - press Esc to stop)"
			} else {
				bodyHeader = "❯ [JSON Body] (Press Enter to edit)"
			}
			reqLines = append(reqLines, selectedStyle.Render(bodyHeader), m.bodyInput.View(), "")
		} else {
			reqLines = append(reqLines, headingStyle.Render(bodyHeader), m.bodyInput.View(), "")
		}
	}

	for len(reqLines) < height {
		reqLines = append(reqLines, "")
	}
	leftPane := lipgloss.JoinVertical(lipgloss.Left, reqLines[:height]...)

	// --- Right Pane: Response View ---
	var respLines []string
	respTitle := headerStyle.Width(halfWidth - 4).Render("Response")
	respLines = append(respLines, respTitle, "")

	if m.loading {
		respLines = append(respLines, "  Sending request...")
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
			respLines = append(respLines, fmt.Sprintf("    %s: %s", k, strings.Join(v, ", ")))
		}
		respLines = append(respLines, "", headingStyle.Render("  [Body]"))

		bodyLines := strings.Split(m.responseBody, "\n")
		maxBodyHeight := height - len(respLines) - 1
		for i := 0; i < len(bodyLines) && i < maxBodyHeight; i++ {
			respLines = append(respLines, "    "+bodyLines[i])
		}
	} else {
		respLines = append(respLines, "  No request sent yet. Press Ctrl+S to send.")
	}

	for len(respLines) < height {
		respLines = append(respLines, "")
	}
	rightPane := lipgloss.JoinVertical(lipgloss.Left, respLines[:height]...)

	return lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(halfWidth).Render(leftPane),
		lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(lipgloss.Color("240")).Width(halfWidth).Render(rightPane),
	)
}

// entry point

func StartAPI(endpoints []frameworks.Endpoint, targetURL string) error {
	model := APIModel{
		endpoints: endpoints,
		filtered:  endpoints,
		targetURL: targetURL,
		screen:    ListScreen,
	}
	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
