package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
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

func substitutePathParams(path string, params []frameworks.PathParam, values []string) string {
	res := path
	for i, p := range params {
		val := ""
		if i < len(values) {
			val = strings.TrimSpace(values[i])
		}
		if val == "" {
			continue
		}
		placeholder := "[" + p.Name + "]"
		if strings.Contains(res, placeholder) {
			// Avoid producing `//` when the path already ends in a slash and
			// the user typed a value with a leading slash. Only touch the
			// unresolved token: external endpoints store full URLs where a
			// `//` after the scheme is meaningful.
			res = strings.ReplaceAll(res, "/"+placeholder, "/"+strings.TrimPrefix(val, "/"))
			res = strings.ReplaceAll(res, placeholder, val)
		}
		rawPlaceholder := fmt.Sprintf("<%s:%s>", p.Type, p.Name)
		res = strings.ReplaceAll(res, rawPlaceholder, val)
		res = strings.ReplaceAll(res, fmt.Sprintf("<%s>", p.Name), val)
		res = strings.ReplaceAll(res, ":"+p.Name, val)
		res = strings.ReplaceAll(res, "*"+p.Name, val)
		res = strings.ReplaceAll(res, "{"+p.Name+"}", val)
	}
	return res
}

// BodyKind is the request body encoding the user picked for an endpoint.
// The empty value means JSON, so caches written before body modes existed keep
// working unchanged.
type BodyKind string

const (
	BodyJSON      BodyKind = "json"
	BodyForm      BodyKind = "form"
	BodyMultipart BodyKind = "multipart"
	BodyRaw       BodyKind = "raw"
)

// normalized maps unknown/legacy values onto the JSON default.
func (k BodyKind) normalized() BodyKind {
	switch k {
	case BodyForm, BodyMultipart, BodyRaw:
		return k
	default:
		return BodyJSON
	}
}

func bodyKindLabel(k BodyKind) string {
	switch k.normalized() {
	case BodyForm:
		return "form"
	case BodyMultipart:
		return "multipart"
	case BodyRaw:
		return "raw"
	default:
		return "JSON"
	}
}

// request persistence cache
type SavedRequest struct {
	PathParams  []string `json:"path_params"`
	QueryParams string   `json:"query_params"`
	Headers     string   `json:"headers"`
	Body        string   `json:"body"`
	BodyKind    BodyKind `json:"body_kind,omitempty"`  // empty ⇒ json
	FileField   string   `json:"file_field,omitempty"` // multipart file part name
	FilePath    string   `json:"file_path,omitempty"`  // multipart file to upload
	HitCount    int      `json:"hit_count"`
	LastCalled  string   `json:"last_called"`
}

// endpointKey identifies a saved request in both the local api_cache.json and
// the global external_endpoints.json.
func endpointKey(method, path string) string {
	return method + ":" + path
}

type APICache map[string]SavedRequest

func loadCache() APICache {
	cache := make(APICache)
	data, err := os.ReadFile(filepath.Join(".cadr", "cache", "api_cache.json"))
	if err == nil {
		_ = json.Unmarshal(data, &cache)
	}
	return cache
}

func saveCache(cache APICache) {
	_ = os.MkdirAll(filepath.Join(".cadr", "cache"), 0755)
	data, err := json.MarshalIndent(cache, "", "  ")
	if err == nil {
		_ = os.WriteFile(filepath.Join(".cadr", "cache", "api_cache.json"), data, 0644)
	}
}

type ExternalEndpointRecord struct {
	Name         string   `json:"name,omitempty"`
	Method       string   `json:"method"`
	Path         string   `json:"path"`
	SavedParams  []string `json:"saved_params,omitempty"`
	SavedQuery   string   `json:"saved_query,omitempty"`
	SavedHeaders string   `json:"saved_headers,omitempty"`
	SavedBody    string   `json:"saved_body,omitempty"`
	SavedKind    BodyKind `json:"saved_body_kind,omitempty"`
	SavedField   string   `json:"saved_file_field,omitempty"`
	SavedPath    string   `json:"saved_file_path,omitempty"`
	HitCount     int      `json:"hit_count,omitempty"`
	LastCalled   string   `json:"last_called,omitempty"`
}

func loadExternalEndpoints() []frameworks.Endpoint {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	extPath := filepath.Join(home, ".cadr", "external_endpoints.json")
	data, err := os.ReadFile(extPath)
	if err != nil {
		return nil
	}

	var records []ExternalEndpointRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil
	}

	var endpoints []frameworks.Endpoint
	for _, r := range records {
		var params []frameworks.PathParam
		reFlask := regexp.MustCompile(`<(?:([a-zA-Z_]\w*):)?([a-zA-Z_]\w*)>`)
		matchesFlask := reFlask.FindAllStringSubmatch(r.Path, -1)
		for _, m := range matchesFlask {
			t := "string"
			if m[1] != "" {
				t = m[1]
			}
			params = append(params, frameworks.PathParam{
				Name: m[2],
				Type: t,
			})
		}

		reFast := regexp.MustCompile(`{([a-zA-Z_]\w*)(?::([a-zA-Z_]\w*))?}`)
		matchesFast := reFast.FindAllStringSubmatch(r.Path, -1)
		for _, m := range matchesFast {
			t := "string"
			if m[2] != "" {
				t = m[2]
			}
			params = append(params, frameworks.PathParam{
				Name: m[1],
				Type: t,
			})
		}

		endpoints = append(endpoints, frameworks.Endpoint{
			Method:      r.Method,
			Path:        r.Path,
			HandlerFunc: r.Name, // Store alias Name in HandlerFunc
			Framework:   "external",
			PathParams:  params,
		})
	}
	return endpoints
}

func readExternalEndpointRecords() ([]ExternalEndpointRecord, string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, ""
	}
	extPath := filepath.Join(home, ".cadr", "external_endpoints.json")
	data, err := os.ReadFile(extPath)
	if err != nil {
		return nil, extPath
	}

	var records []ExternalEndpointRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, extPath
	}
	return records, extPath
}

func savedRequestFromExternalRecord(r ExternalEndpointRecord) SavedRequest {
	return SavedRequest{
		PathParams:  r.SavedParams,
		QueryParams: r.SavedQuery,
		Headers:     r.SavedHeaders,
		Body:        r.SavedBody,
		BodyKind:    r.SavedKind,
		FileField:   r.SavedField,
		FilePath:    r.SavedPath,
		HitCount:    r.HitCount,
		LastCalled:  r.LastCalled,
	}
}

func saveExternalEndpointPersistence(method, urlStr string, saved SavedRequest) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	cadrDir := filepath.Join(home, ".cadr")
	_ = os.MkdirAll(cadrDir, 0755)
	extPath := filepath.Join(cadrDir, "external_endpoints.json")

	var records []ExternalEndpointRecord
	data, err := os.ReadFile(extPath)
	if err == nil {
		_ = json.Unmarshal(data, &records)
	}

	for i, r := range records {
		if r.Method == method && r.Path == urlStr {
			records[i].SavedParams = saved.PathParams
			records[i].SavedQuery = saved.QueryParams
			records[i].SavedHeaders = saved.Headers
			records[i].SavedBody = saved.Body
			records[i].SavedKind = saved.BodyKind.normalized()
			records[i].SavedField = saved.FileField
			records[i].SavedPath = saved.FilePath
			records[i].HitCount++
			records[i].LastCalled = time.Now().Format(time.RFC3339)
			break
		}
	}

	newData, err := json.MarshalIndent(records, "", "  ")
	if err == nil {
		_ = os.WriteFile(extPath, newData, 0644)
	}
}

func loadExternalEndpointPersistence(method, urlStr string) (SavedRequest, bool) {
	records, _ := readExternalEndpointRecords()
	for _, r := range records {
		if r.Method == method && r.Path == urlStr {
			return savedRequestFromExternalRecord(r), true
		}
	}
	return SavedRequest{}, false
}

// loadExternalSavedRequests returns every saved external request keyed by
// endpointKey, so the list view can render hit counts without re-reading the
// file per row.
func loadExternalSavedRequests() map[string]SavedRequest {
	saved := make(map[string]SavedRequest)
	records, _ := readExternalEndpointRecords()
	for _, r := range records {
		saved[endpointKey(r.Method, r.Path)] = savedRequestFromExternalRecord(r)
	}
	return saved
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
	GinURL     string
	NodeURL    string

	// RequestTimeout caps a single API call. Zero means the built-in default
	// (10s), so models constructed in tests keep working.
	RequestTimeout time.Duration
}

// defaultRequestTimeout matches the historical hardcoded value in sendRequest.
const defaultRequestTimeout = 10 * time.Second

func (c APIConfig) requestTimeout() time.Duration {
	if c.RequestTimeout > 0 {
		return c.RequestTimeout
	}
	return defaultRequestTimeout
}

// TUI model definition
type APIModel struct {
	endpoints           []frameworks.Endpoint
	filtered            []frameworks.Endpoint
	selectedIdx         int
	activeSection       int // 0: Local/Discovered, 1: Global/External
	externalEndpoints   []frameworks.Endpoint
	filteredExternal    []frameworks.Endpoint
	selectedExternalIdx int
	apiConfig           APIConfig
	graph               *graph.Graph

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
	tempFileToOpen       string
	tempFileIsBody       bool

	// form & interactive input states
	focusIdx       int
	formEditing    bool
	selectedAll    bool
	bodyKind       BodyKind
	pathInputs     []textinput.Model
	queryInput     textinput.Model
	headerInput    textinput.Model
	bodyInput      textarea.Model
	fileFieldInput textinput.Model
	filePathInput  textinput.Model

	// saved request caches, refreshed after every save instead of being
	// re-read from disk per rendered row
	cache         APICache
	externalCache map[string]SavedRequest

	// background http runner states
	loading         bool
	latency         time.Duration
	statusCode      int
	responseBody    string
	responseErr     string
	responseHeaders http.Header
}

type responseMsg struct {
	statusCode int
	latency    time.Duration
	body       string
	headers    http.Header
	err        error
	timedOut   bool
	timeout    time.Duration
}

func (m APIModel) Init() tea.Cmd {
	return nil
}

// input management helpers
func (m *APIModel) initForm() {
	ep := m.getCurrentEndpoint()
	m.focusIdx = 0
	m.formEditing = false

	var saved SavedRequest
	var hasSaved bool
	if ep.Framework == "external" {
		saved, hasSaved = m.externalCache[endpointKey(ep.Method, ep.Path)]
	} else {
		saved, hasSaved = m.cache[endpointKey(ep.Method, ep.Path)]
	}

	halfWidth := m.width / 2
	if halfWidth <= 0 {
		halfWidth = 40
	}

	m.bodyKind = BodyJSON
	if hasSaved {
		m.bodyKind = saved.BodyKind.normalized()
	}

	// dynamic path parameters fields mapping
	m.pathInputs = make([]textinput.Model, len(ep.PathParams))
	for i, p := range ep.PathParams {
		ti := textinput.New()
		if p.Type == frameworks.UnresolvedPathParamType {
			ti.Placeholder = "unresolved in source — e.g. /api/v1"
		} else {
			ti.Placeholder = fmt.Sprintf("type: %s", p.Type)
		}
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
	m.bodyInput.SetWidth(halfWidth - 4)
	m.bodyInput.SetHeight(4)
	if hasSaved {
		m.bodyInput.SetValue(saved.Body)
	}

	m.fileFieldInput = textinput.New()
	m.fileFieldInput.Placeholder = "file"
	m.fileFieldInput.Prompt = "  "
	if hasSaved {
		m.fileFieldInput.SetValue(saved.FileField)
	}

	m.filePathInput = textinput.New()
	m.filePathInput.Placeholder = "./photo.png"
	m.filePathInput.Prompt = "  "
	if hasSaved {
		m.filePathInput.SetValue(saved.FilePath)
	}

	m.applyBodyKindPresentation()

	m.responseBody = ""
	m.responseErr = ""
	m.statusCode = 0
	m.loading = false
	m.responseScrollY = 0
	m.responseSearchActive = false
	m.responseSearchVal = ""
	m.updateFocus()
}

// applyBodyKindPresentation keeps the shared body textarea's placeholder in
// sync with the selected mode.
func (m *APIModel) applyBodyKindPresentation() {
	switch m.bodyKind.normalized() {
	case BodyForm:
		m.bodyInput.Placeholder = "key=value (one per line)"
	case BodyMultipart:
		m.bodyInput.Placeholder = "key=value fields (optional)"
	case BodyRaw:
		m.bodyInput.Placeholder = "raw body (text/plain unless a Content-Type header is set)"
	default:
		m.bodyInput.Placeholder = `{"key": "value"}`
	}
}

// cycleBodyKind advances json → form → multipart → raw → json. Only methods
// that accept a body have a body section, so the key is a no-op otherwise.
func (m *APIModel) cycleBodyKind() {
	if !m.methodSupportsBody() {
		return
	}
	switch m.bodyKind.normalized() {
	case BodyJSON:
		m.bodyKind = BodyForm
	case BodyForm:
		m.bodyKind = BodyMultipart
	case BodyMultipart:
		m.bodyKind = BodyRaw
	default:
		m.bodyKind = BodyJSON
	}
	m.applyBodyKindPresentation()
	if m.focusIdx >= m.fieldCount() {
		m.focusIdx = m.fieldCount() - 1
	}
	m.updateFocus()
}

// field index helpers — focus is a single integer over a dynamic row list, so
// every site must agree on where Query/Headers/Body/File rows start.
func (m *APIModel) queryIdx() int { return len(m.pathInputs) }

func (m *APIModel) headerIdx() int { return len(m.pathInputs) + 1 }

// bodyFieldIdx is -1 when the current method takes no body.
func (m *APIModel) bodyFieldIdx() int {
	if !m.methodSupportsBody() {
		return -1
	}
	return len(m.pathInputs) + 2
}

// fileFieldIdx and filePathIdx only exist in multipart mode.
func (m *APIModel) fileFieldIdx() int {
	if m.bodyFieldIdx() < 0 || m.bodyKind.normalized() != BodyMultipart {
		return -1
	}
	return len(m.pathInputs) + 3
}

func (m *APIModel) filePathIdx() int {
	if m.fileFieldIdx() < 0 {
		return -1
	}
	return len(m.pathInputs) + 4
}

func (m *APIModel) fieldCount() int {
	count := len(m.pathInputs) + 2 // query + headers
	if m.methodSupportsBody() {
		count++
	}
	if m.fileFieldIdx() >= 0 {
		count += 2
	}
	return count
}

func (m *APIModel) updateFocus() {
	for i := range m.pathInputs {
		m.pathInputs[i].Blur()
	}
	m.queryInput.Blur()
	m.headerInput.Blur()
	m.bodyInput.Blur()
	m.fileFieldInput.Blur()
	m.filePathInput.Blur()

	if !m.formEditing {
		return
	}

	if m.focusIdx < len(m.pathInputs) {
		m.pathInputs[m.focusIdx].Focus()
		return
	}

	switch m.focusIdx {
	case m.queryIdx():
		m.queryInput.Focus()
	case m.headerIdx():
		m.headerInput.Focus()
	case m.bodyFieldIdx():
		m.bodyInput.Focus()
	case m.fileFieldIdx():
		m.fileFieldInput.Focus()
	case m.filePathIdx():
		m.filePathInput.Focus()
	}
}

func (m *APIModel) focusInput(idx int) {
	maxIdx := m.fieldCount() - 1

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

	for i := range m.pathInputs {
		m.pathInputs[i].TextStyle = defaultStyle
	}
	m.queryInput.TextStyle = defaultStyle
	m.headerInput.TextStyle = defaultStyle
	m.fileFieldInput.TextStyle = defaultStyle
	m.filePathInput.TextStyle = defaultStyle
	m.bodyInput.FocusedStyle.Text = defaultStyle

	if !val {
		return
	}

	switch {
	case m.focusIdx < len(m.pathInputs):
		m.pathInputs[m.focusIdx].TextStyle = highlightStyle
	case m.focusIdx == m.queryIdx():
		m.queryInput.TextStyle = highlightStyle
	case m.focusIdx == m.headerIdx():
		m.headerInput.TextStyle = highlightStyle
	case m.focusIdx == m.bodyFieldIdx():
		m.bodyInput.FocusedStyle.Text = highlightStyle
	case m.focusIdx == m.fileFieldIdx():
		m.fileFieldInput.TextStyle = highlightStyle
	case m.focusIdx == m.filePathIdx():
		m.filePathInput.TextStyle = highlightStyle
	}
}

func (m *APIModel) methodSupportsBody() bool {
	switch strings.ToUpper(m.getCurrentEndpoint().Method) {
	case "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		return true
	}
	return false
}

func (m *APIModel) resolveTargetURL(ep frameworks.Endpoint) string {
	switch ep.Framework {
	case "flask":
		return m.apiConfig.FlaskURL
	case "fastapi":
		return m.apiConfig.FastAPIURL
	case "gin":
		return m.apiConfig.GinURL
	case "node":
		if m.apiConfig.NodeURL != "" {
			return m.apiConfig.NodeURL
		}
		return "http://localhost:3000"
	}
	return m.apiConfig.DefaultURL
}

func (m *APIModel) getCurrentEndpoint() frameworks.Endpoint {
	if m.activeSection == 1 {
		if len(m.filteredExternal) > 0 && m.selectedExternalIdx < len(m.filteredExternal) {
			return m.filteredExternal[m.selectedExternalIdx]
		}
	} else {
		if len(m.filtered) > 0 && m.selectedIdx < len(m.filtered) {
			return m.filtered[m.selectedIdx]
		}
	}
	return frameworks.Endpoint{}
}

func (m *APIModel) getFinalURL() string {
	ep := m.getCurrentEndpoint()
	var vals []string
	for _, ti := range m.pathInputs {
		vals = append(vals, ti.Value())
	}
	path := substitutePathParams(ep.Path, ep.PathParams, vals)

	var urlStr string
	if ep.Framework == "external" {
		urlStr = path
	} else {
		target := m.resolveTargetURL(ep)
		urlStr = joinPath(target, path)
	}

	qVal := strings.TrimSpace(m.queryInput.Value())
	if qVal != "" {
		if strings.HasPrefix(qVal, "?") {
			urlStr += qVal
		} else {
			urlStr += "?" + qVal
		}
	}
	return urlStr
}

func (m *APIModel) applyFilter() {
	if m.searchVal == "" {
		m.filtered = m.endpoints
		m.filteredExternal = m.externalEndpoints
	} else {
		q := strings.ToLower(m.searchVal)
		var filtered []frameworks.Endpoint
		for _, ep := range m.endpoints {
			if strings.Contains(strings.ToLower(ep.Path), q) || strings.Contains(strings.ToLower(ep.Method), q) {
				filtered = append(filtered, ep)
			}
		}
		m.filtered = filtered

		var filteredExt []frameworks.Endpoint
		for _, ep := range m.externalEndpoints {
			if strings.Contains(strings.ToLower(ep.Path), q) || strings.Contains(strings.ToLower(ep.Method), q) || strings.Contains(strings.ToLower(ep.HandlerFunc), q) {
				filteredExt = append(filteredExt, ep)
			}
		}
		m.filteredExternal = filteredExt
	}
	m.selectedIdx = 0
	m.selectedExternalIdx = 0
}

// requestSpec is everything needed to perform one API call. It is built from
// the form or from a saved request, so both send sites share one code path.
type requestSpec struct {
	Method    string
	URL       string
	Headers   string // "Name: value" per line (comma-separated also accepted)
	BodyKind  BodyKind
	Body      string // json text | key=value lines | raw text
	FileField string
	FilePath  string
	Timeout   time.Duration
}

// maxResponseBytes caps how much of a response body is kept in memory; larger
// bodies are truncated with a notice in the response pane.
const maxResponseBytes = 5 << 20 // 5 MiB

// http request runner
func sendRequest(spec requestSpec) tea.Cmd {
	return func() tea.Msg {
		start := time.Now()

		body, contentType, err := buildRequestBody(spec)
		if err != nil {
			return responseMsg{err: err}
		}

		req, err := http.NewRequestWithContext(context.Background(), spec.Method, spec.URL, body)
		if err != nil {
			return responseMsg{err: err}
		}

		// A user-supplied Content-Type always wins over the mode-derived one.
		hasUserContentType := false
		for _, header := range parseHeaderLines(spec.Headers) {
			if strings.EqualFold(header[0], "Content-Type") {
				hasUserContentType = true
			}
			req.Header.Set(header[0], header[1])
		}
		if !hasUserContentType && contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}

		timeout := spec.Timeout
		if timeout <= 0 {
			timeout = defaultRequestTimeout
		}
		client := &http.Client{Timeout: timeout}

		resp, err := client.Do(req)
		if err != nil {
			if isTimeoutError(err) {
				return responseMsg{err: err, timedOut: true, timeout: timeout, latency: time.Since(start)}
			}
			return responseMsg{err: err, latency: time.Since(start)}
		}
		defer resp.Body.Close()

		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		notice := ""
		if len(respBody) > maxResponseBytes {
			respBody = respBody[:maxResponseBytes]
			notice = fmt.Sprintf("\n\n[response truncated at %d MiB]", maxResponseBytes>>20)
		}
		if readErr != nil && len(respBody) == 0 {
			return responseMsg{err: readErr, latency: time.Since(start)}
		}

		return responseMsg{
			statusCode: resp.StatusCode,
			latency:    time.Since(start),
			body:       string(respBody) + notice,
			headers:    resp.Header,
		}
	}
}

// buildRequestBody encodes the body for the selected mode and returns the
// Content-Type that mode implies. A nil reader means "no body".
func buildRequestBody(spec requestSpec) (io.Reader, string, error) {
	switch spec.BodyKind.normalized() {
	case BodyForm:
		if spec.Body == "" {
			return nil, "application/x-www-form-urlencoded", nil
		}
		return strings.NewReader(encodeFormBody(spec.Body)), "application/x-www-form-urlencoded", nil
	case BodyMultipart:
		buf := &bytes.Buffer{}
		writer := multipart.NewWriter(buf)
		for _, kv := range parseKeyValueLines(spec.Body) {
			if err := writer.WriteField(kv[0], kv[1]); err != nil {
				return nil, "", fmt.Errorf("multipart: %w", err)
			}
		}

		filePath := strings.TrimSpace(spec.FilePath)
		if filePath != "" {
			fieldName := strings.TrimSpace(spec.FileField)
			if fieldName == "" {
				return nil, "", fmt.Errorf("multipart: set a file field name for %s", filePath)
			}
			file, err := os.Open(filePath)
			if err != nil {
				return nil, "", fmt.Errorf("multipart: cannot read %s: %w", filePath, err)
			}
			defer file.Close()

			part, err := writer.CreateFormFile(fieldName, filepath.Base(filePath))
			if err != nil {
				return nil, "", fmt.Errorf("multipart: %w", err)
			}
			if _, err := io.Copy(part, file); err != nil {
				return nil, "", fmt.Errorf("multipart: %w", err)
			}
		}

		contentType := writer.FormDataContentType()
		if err := writer.Close(); err != nil {
			return nil, "", fmt.Errorf("multipart: %w", err)
		}
		return buf, contentType, nil
	case BodyRaw:
		if spec.Body == "" {
			return nil, "text/plain", nil
		}
		return strings.NewReader(spec.Body), "text/plain", nil
	default:
		if spec.Body == "" {
			return nil, "application/json", nil
		}
		return strings.NewReader(spec.Body), "application/json", nil
	}
}

// parseKeyValueLines reads `key=value` lines (form fields and multipart text
// parts). Blank lines and # comments are ignored.
func parseKeyValueLines(body string) [][2]string {
	var pairs [][2]string
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		pairs = append(pairs, [2]string{strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])})
	}
	return pairs
}

func encodeFormBody(body string) string {
	values := url.Values{}
	for _, kv := range parseKeyValueLines(body) {
		values.Add(kv[0], kv[1])
	}
	return values.Encode()
}

// parseHeaderLines accepts one header per line and tolerates the historical
// comma-separated form. A comma only starts a new header when it is followed
// by `Name:`, so values like `Accept: a, b` stay intact.
func parseHeaderLines(headersStr string) [][2]string {
	var headers [][2]string
	for _, line := range strings.Split(strings.ReplaceAll(headersStr, "\r\n", "\n"), "\n") {
		for _, part := range splitHeaderLine(line) {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			kv := strings.SplitN(part, ":", 2)
			if len(kv) != 2 {
				continue
			}
			name := strings.TrimSpace(kv[0])
			if name == "" {
				continue
			}
			headers = append(headers, [2]string{name, strings.TrimSpace(kv[1])})
		}
	}
	return headers
}

func splitHeaderLine(line string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(line); i++ {
		if line[i] != ',' {
			continue
		}
		if looksLikeHeaderStart(line[i+1:]) {
			parts = append(parts, line[start:i])
			start = i + 1
		}
	}
	parts = append(parts, line[start:])
	return parts
}

func looksLikeHeaderStart(s string) bool {
	s = strings.TrimLeft(s, " \t")
	colon := strings.IndexByte(s, ':')
	if colon <= 0 {
		return false
	}
	for i := 0; i < colon; i++ {
		c := s[i]
		isNameChar := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '_'
		if !isNameChar {
			return false
		}
	}
	return true
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

// persistence helpers shared by the send paths
func (m *APIModel) refreshCaches() {
	m.cache = loadCache()
	m.externalCache = loadExternalSavedRequests()
}

func (m *APIModel) bodyValue() string {
	if !m.methodSupportsBody() {
		return ""
	}
	return m.bodyInput.Value()
}

func (m *APIModel) currentSavedRequest() SavedRequest {
	pathVals := make([]string, 0, len(m.pathInputs))
	for _, ti := range m.pathInputs {
		pathVals = append(pathVals, ti.Value())
	}
	return SavedRequest{
		PathParams:  pathVals,
		QueryParams: m.queryInput.Value(),
		Headers:     m.headerInput.Value(),
		Body:        m.bodyValue(),
		BodyKind:    m.bodyKind.normalized(),
		FileField:   m.fileFieldInput.Value(),
		FilePath:    m.filePathInput.Value(),
	}
}

func (m *APIModel) persistCurrentRequest(ep frameworks.Endpoint, saved SavedRequest) {
	if ep.Framework == "external" {
		saveExternalEndpointPersistence(ep.Method, ep.Path, saved)
		m.refreshCaches()
		return
	}

	if m.cache == nil {
		m.cache = loadCache()
	}
	saved.HitCount++
	saved.LastCalled = time.Now().Format(time.RFC3339)
	m.cache[endpointKey(ep.Method, ep.Path)] = saved
	saveCache(m.cache)
}

func (m *APIModel) requestSpecFromSaved(ep frameworks.Endpoint, urlStr string, saved SavedRequest) requestSpec {
	return requestSpec{
		Method:    ep.Method,
		URL:       urlStr,
		Headers:   saved.Headers,
		BodyKind:  saved.BodyKind.normalized(),
		Body:      saved.Body,
		FileField: saved.FileField,
		FilePath:  saved.FilePath,
		Timeout:   m.apiConfig.requestTimeout(),
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
			case "enter":
				tmpDir := filepath.Join(".cadr", "tmp")
				_ = os.MkdirAll(tmpDir, 0755)
				f, err := os.CreateTemp(tmpDir, "response-*.json")
				if err == nil {
					_, _ = f.WriteString(m.responseBody)
					f.Close()
					m.tempFileToOpen = f.Name()
					m.tempFileIsBody = false
					return m, tea.Quit
				}
				return m, nil
			case "y":
				_ = clipboard.WriteAll(m.responseBody)
				return m, nil
			case "j", "down":
				halfWidth := m.width / 2
				rightWidth := m.width - halfWidth
				width := rightWidth - 4

				var targetHost string
				epSelected := m.getCurrentEndpoint()
				if epSelected.Method != "" {
					if epSelected.Framework == "external" {
						targetHost = epSelected.Path
					} else {
						targetHost = m.resolveTargetURL(epSelected)
					}
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
				if m.activeSection == 1 {
					if m.selectedExternalIdx < len(m.filteredExternal)-1 {
						m.selectedExternalIdx++
					}
				} else {
					if m.selectedIdx < len(m.filtered)-1 {
						m.selectedIdx++
					}
				}
			case "k", "up":
				if m.activeSection == 1 {
					if m.selectedExternalIdx > 0 {
						m.selectedExternalIdx--
					}
				} else {
					if m.selectedIdx > 0 {
						m.selectedIdx--
					}
				}
			case "[":
				m.activeSection = 0
				ep := m.getCurrentEndpoint()
				if ep.Framework == "external" {
					m.rightTab = TabResponse
				}
			case "]":
				m.activeSection = 1
				m.rightTab = TabResponse
			case "l", "right":
				ep := m.getCurrentEndpoint()
				if ep.Framework != "external" && m.rightTab == TabCallGraph {
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
				ep := m.getCurrentEndpoint()
				if ep.Framework != "external" {
					if m.rightTab == TabResponse {
						m.rightTab = TabCallGraph
					} else {
						m.rightTab = TabResponse
					}
				}
			case "/":
				m.searchActive = true
				m.searchVal = ""
			case "enter":
				ep := m.getCurrentEndpoint()
				if ep.Method != "" {
					m.screen = FormScreen
					m.initForm()
				}
			case "ctrl+s":
				ep := m.getCurrentEndpoint()
				if ep.Method != "" {
					saved, hasSaved := m.cache[endpointKey(ep.Method, ep.Path)]
					if ep.Framework == "external" {
						saved, hasSaved = m.externalCache[endpointKey(ep.Method, ep.Path)]
					}

					if !hasSaved {
						saved = SavedRequest{
							PathParams: make([]string, len(ep.PathParams)),
						}
					}

					path := substitutePathParams(ep.Path, ep.PathParams, saved.PathParams)

					var urlStr string
					if ep.Framework == "external" {
						urlStr = path
					} else {
						target := m.resolveTargetURL(ep)
						urlStr = joinPath(target, path)
					}

					if saved.QueryParams != "" {
						urlStr += "?" + saved.QueryParams
					}

					m.loading = true
					m.responseErr = ""
					m.rightTab = TabResponse
					m.responseScrollY = 0

					m.persistCurrentRequest(ep, saved)
					return m, sendRequest(m.requestSpecFromSaved(ep, urlStr, saved))
				}
			}
			return m, nil
		}

		if m.screen == FormScreen {
			if m.formEditing {
				// special key overrides for the body editor
				if m.focusIdx == m.bodyFieldIdx() {
					if key == "tab" {
						m.bodyInput.InsertString("  ")
						return m, nil
					}
					if key == "ctrl+e" {
						tmpDir := filepath.Join(".cadr", "tmp")
						_ = os.MkdirAll(tmpDir, 0755)
						f, err := os.CreateTemp(tmpDir, "request-*.json")
						if err == nil {
							_, _ = f.WriteString(m.bodyInput.Value())
							f.Close()
							m.tempFileToOpen = f.Name()
							m.tempFileIsBody = true
							return m, tea.Quit
						}
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
				case "enter":
					if m.focusIdx != m.bodyFieldIdx() {
						m.formEditing = false
						m.updateFocus()
						return m, nil
					}
				case "ctrl+s":
					m.loading = true
					ep := m.getCurrentEndpoint()
					saved := m.currentSavedRequest()
					urlStr := m.getFinalURL()
					m.persistCurrentRequest(ep, saved)

					m.rightTab = TabResponse
					m.responseErr = ""
					m.responseScrollY = 0

					return m, sendRequest(m.requestSpecFromSaved(ep, urlStr, saved))
				}
			} else {
				switch key {
				case "esc", "h", "H", "q":
					m.screen = ListScreen
					return m, nil
				case "l", "right":
					ep := m.getCurrentEndpoint()
					if ep.Framework != "external" && m.rightTab == TabCallGraph {
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
					ep := m.getCurrentEndpoint()
					if ep.Framework != "external" {
						if m.rightTab == TabResponse {
							m.rightTab = TabCallGraph
						} else {
							m.rightTab = TabResponse
						}
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
				case "b":
					m.cycleBodyKind()
					return m, nil
				case "ctrl+e":
					if m.focusIdx == m.bodyFieldIdx() {
						tmpDir := filepath.Join(".cadr", "tmp")
						_ = os.MkdirAll(tmpDir, 0755)
						f, err := os.CreateTemp(tmpDir, "request-*.json")
						if err == nil {
							_, _ = f.WriteString(m.bodyInput.Value())
							f.Close()
							m.tempFileToOpen = f.Name()
							m.tempFileIsBody = true
							return m, tea.Quit
						}
					}
					return m, nil
				case "ctrl+s":
					m.loading = true
					ep := m.getCurrentEndpoint()
					saved := m.currentSavedRequest()
					urlStr := m.getFinalURL()
					m.persistCurrentRequest(ep, saved)

					m.rightTab = TabResponse
					m.responseErr = ""
					m.responseScrollY = 0

					return m, sendRequest(m.requestSpecFromSaved(ep, urlStr, saved))
				}
			}
		}

	case responseMsg:
		m.loading = false
		if msg.err != nil {
			// Failures are not HTTP responses: keep the status empty instead of
			// faking a 500, so a dead server never looks like a real error reply.
			m.statusCode = 0
			m.latency = msg.latency
			m.responseHeaders = nil

			ep := m.getCurrentEndpoint()
			target := m.resolveTargetURL(ep)
			if ep.Framework == "external" {
				target = ep.Path
			}

			detail := msg.err.Error()
			if msg.timedOut {
				detail = fmt.Sprintf("timed out after %s — raise api_timeout in .cadr/config.yaml", msg.timeout)
			}
			m.responseErr = fmt.Sprintf("%s\n\nIs your API server running at %s?", detail, target)
			m.responseBody = ""
		} else {
			m.statusCode = msg.statusCode
			m.latency = msg.latency
			m.responseErr = ""
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
						switch {
						case m.focusIdx < len(m.pathInputs):
							m.pathInputs[m.focusIdx].SetValue("")
						case m.focusIdx == m.queryIdx():
							m.queryInput.SetValue("")
						case m.focusIdx == m.headerIdx():
							m.headerInput.SetValue("")
						case m.focusIdx == m.bodyFieldIdx():
							m.bodyInput.SetValue("")
						case m.focusIdx == m.fileFieldIdx():
							m.fileFieldInput.SetValue("")
						case m.focusIdx == m.filePathIdx():
							m.filePathInput.SetValue("")
						}
					}
					m.setSelectedAll(false)
				}
			}
		}

		switch {
		case m.focusIdx < len(m.pathInputs):
			m.pathInputs[m.focusIdx], cmd = m.pathInputs[m.focusIdx].Update(msg)
			cmds = append(cmds, cmd)
		case m.focusIdx == m.queryIdx():
			m.queryInput, cmd = m.queryInput.Update(msg)
			cmds = append(cmds, cmd)
		case m.focusIdx == m.headerIdx():
			m.headerInput, cmd = m.headerInput.Update(msg)
			cmds = append(cmds, cmd)
		case m.focusIdx == m.bodyFieldIdx():
			m.bodyInput, cmd = m.bodyInput.Update(msg)
			cmds = append(cmds, cmd)
		case m.focusIdx == m.fileFieldIdx():
			m.fileFieldInput, cmd = m.fileFieldInput.Update(msg)
			cmds = append(cmds, cmd)
		case m.focusIdx == m.filePathIdx():
			m.filePathInput, cmd = m.filePathInput.Update(msg)
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
	epSelected := m.getCurrentEndpoint()
	if epSelected.Method != "" {
		if epSelected.Framework == "external" {
			targetHost = epSelected.Path
		} else {
			targetHost = m.resolveTargetURL(epSelected)
		}
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
				helpBar = faintStyle.Render(" j/k: scroll response · enter: open in editor · /: search · y: copy body · h/left: back to left pane · t/tab: toggle tab · q: quit")
			}
		} else {
			helpBar = faintStyle.Render(" j/k: scroll graph · enter: open file in Neovim · h/left: back to left pane · t/tab: toggle tab · q: quit")
		}
	} else if m.screen == ListScreen {
		helpBar = faintStyle.Render(" j/k: navigate · enter: request builder · ctrl+s: execute · t/tab: toggle tab · q: quit")
	} else {
		if m.formEditing {
			if m.focusIdx == m.bodyFieldIdx() {
				helpBar = faintStyle.Render(" esc: stop editing · tab: indent · ctrl+e: open in editor · ctrl+s: execute request")
			} else {
				helpBar = faintStyle.Render(" esc: stop editing · ctrl+s: execute request")
			}
		} else {
			if m.focusIdx == m.bodyFieldIdx() {
				helpBar = faintStyle.Render(" q/H: back to list · b: cycle body mode · j/k: navigate fields · enter: edit · ctrl+e: open in editor · ctrl+s: execute request")
			} else {
				helpBar = faintStyle.Render(" q/H: back to list · j/k: navigate fields · enter: edit field · t/tab: toggle tab · ctrl+s: execute request")
			}
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
	if epSelected.Framework != "external" && m.rightTab == TabCallGraph {
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

	itemsPerPage := (height - 2) / 4
	if itemsPerPage <= 0 {
		itemsPerPage = 1
	}

	if m.activeSection == 0 {
		localHeader := "Local Endpoints"
		if m.searchActive {
			localHeader = fmt.Sprintf("Local Endpoints (Search: %s_)", m.searchVal)
		}
		lines = append(lines, apiHeaderStyle.Width(width).Render(localHeader))
		lines = append(lines, "")

		if len(m.filtered) == 0 {
			lines = append(lines, faintStyle.Render("  No local endpoints found."))
		} else {
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

				saved, hasSaved := m.cache[endpointKey(ep.Method, ep.Path)]
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
	} else {
		globalHeader := "Global Endpoints"
		if m.searchActive {
			globalHeader = fmt.Sprintf("Global Endpoints (Search: %s_)", m.searchVal)
		}
		lines = append(lines, apiHeaderStyle.Width(width).Render(globalHeader))
		lines = append(lines, "")

		if len(m.filteredExternal) == 0 {
			lines = append(lines, faintStyle.Render("  No global endpoints found."))
		} else {
			start := 0
			if len(m.filteredExternal) > itemsPerPage {
				if m.selectedExternalIdx >= itemsPerPage/2 {
					start = m.selectedExternalIdx - itemsPerPage/2
				}
				if start+itemsPerPage > len(m.filteredExternal) {
					start = len(m.filteredExternal) - itemsPerPage
				}
				if start < 0 {
					start = 0
				}
			}

			for i := start; i < len(m.filteredExternal) && i < start+itemsPerPage; i++ {
				ep := m.filteredExternal[i]
				methodPrefix := "  "
				if !m.focusRight && i == m.selectedExternalIdx {
					methodPrefix = "> "
				}

				method := methodStyle(ep.Method).Render(ep.Method)
				var line2, line3 string
				saved, hasSaved := m.externalCache[endpointKey(ep.Method, ep.Path)]

				if ep.HandlerFunc != "" {
					line2 = ep.HandlerFunc
					line3 = ep.Path
				} else {
					line2 = ep.Path
					if hasSaved && saved.HitCount > 0 {
						timeStr := "-"
						if saved.LastCalled != "" {
							t, err := time.Parse(time.RFC3339, saved.LastCalled)
							if err == nil {
								timeStr = formatRelativeTime(t)
							}
						}
						line3 = fmt.Sprintf("  • %d hits • %s", saved.HitCount, timeStr)
					} else {
						line3 = ""
					}
				}

				if len(line2) > width-2 {
					line2 = line2[:width-5] + "…"
				}
				if !m.focusRight && i == m.selectedExternalIdx {
					line2 = selectedStyle.Render(line2)
				}

				if line3 != "" && !strings.HasPrefix(line3, "  •") {
					if len(line3) > width-2 {
						line3 = line3[:width-5] + "…"
					}
					line3 = faintStyle.Render("  " + line3)
				} else if line3 != "" {
					if len(line3) > width-2 {
						line3 = line3[:width-5] + "…"
					}
					line3 = faintStyle.Render(line3)
				}

				lines = append(lines, fmt.Sprintf("%s%s", methodPrefix, method))
				lines = append(lines, fmt.Sprintf("  %s", line2))
				if line3 != "" {
					lines = append(lines, line3)
				} else {
					lines = append(lines, "")
				}
				lines = append(lines, "")
			}
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

	ep := m.getCurrentEndpoint()

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
	if !m.focusRight && m.focusIdx == m.queryIdx() {
		queryPrefix = "> "
		queryHeaderStyle = selectedStyle
	}
	m.queryInput.Prompt = "  "
	reqLines = append(reqLines, queryHeaderStyle.Render(fmt.Sprintf("%sQuery params:", queryPrefix)))
	reqLines = append(reqLines, m.queryInput.View())
	reqLines = append(reqLines, "")

	headerPrefix := "  "
	headerHeaderStyle := headingStyle
	if !m.focusRight && m.focusIdx == m.headerIdx() {
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
		bodyText := fmt.Sprintf("Body (%s)  [b: cycle]", bodyKindLabel(m.bodyKind))
		if !m.focusRight && m.focusIdx == m.bodyFieldIdx() {
			bodyPrefix = "> "
			bodyHeaderStyle = selectedStyle
			if m.formEditing {
				bodyText = fmt.Sprintf("Body (%s) (Editing):", bodyKindLabel(m.bodyKind))
			} else {
				bodyText = fmt.Sprintf("Body (%s) (Enter to edit · b: cycle):", bodyKindLabel(m.bodyKind))
			}
		}
		reqLines = append(reqLines, bodyHeaderStyle.Render(fmt.Sprintf("%s%s", bodyPrefix, bodyText)))
		reqLines = append(reqLines, m.bodyInput.View())

		if m.bodyKind.normalized() == BodyMultipart {
			m.fileFieldInput.Prompt = "  "
			fileFieldPrefix := "  "
			fileFieldStyle := headingStyle
			if !m.focusRight && m.focusIdx == m.fileFieldIdx() {
				fileFieldPrefix = "> "
				fileFieldStyle = selectedStyle
			}
			reqLines = append(reqLines, fileFieldStyle.Render(fmt.Sprintf("%sFile field:", fileFieldPrefix)))
			reqLines = append(reqLines, m.fileFieldInput.View())

			m.filePathInput.Prompt = "  "
			filePathPrefix := "  "
			filePathStyle := headingStyle
			if !m.focusRight && m.focusIdx == m.filePathIdx() {
				filePathPrefix = "> "
				filePathStyle = selectedStyle
			}
			reqLines = append(reqLines, filePathStyle.Render(fmt.Sprintf("%sFile path (blank = fields only):", filePathPrefix)))
			reqLines = append(reqLines, m.filePathInput.View())
		}
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
	if m.statusCode <= 0 && m.responseErr == "" {
		return 0
	}
	if m.responseErr != "" {
		count := 3 // status, empty, [Error]
		for _, line := range strings.Split(m.responseErr, "\n") {
			count += len(wrapLine(line, width-4))
		}
		return count
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

	var currentHeaderStyle lipgloss.Style
	if m.focusRight && m.rightTab == TabResponse {
		currentHeaderStyle = apiHeaderStyle.Copy().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("15")).
			Bold(true)
	} else {
		currentHeaderStyle = apiHeaderStyle
	}

	var respTitle string
	if m.responseSearchActive {
		respTitle = currentHeaderStyle.Width(width).Render(fmt.Sprintf("Response (Search: %s_)", m.responseSearchVal))
	} else {
		respTitle = currentHeaderStyle.Width(width).Render("Response")
	}
	respLines = append(respLines, respTitle, "")

	if m.loading {
		respLines = append(respLines, faintStyle.Render("  Sending request..."))
	} else if m.responseErr != "" {
		failureStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
		respLines = append(respLines,
			fmt.Sprintf("  Status:  %s", failureStyle.Render("request failed")),
			"",
			headingStyle.Render("  [Error]"),
		)
		for _, line := range strings.Split(strings.ReplaceAll(m.responseErr, "\r", ""), "\n") {
			wrapped := wrapLine(line, width-4)
			for _, wl := range wrapped {
				highlighted := wl
				if m.responseSearchVal != "" {
					highlighted = highlightMatches(wl, m.responseSearchVal)
				}
				respLines = append(respLines, "    "+highlighted)
			}
		}
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
	ep := m.getCurrentEndpoint()
	if ep.Method == "" || ep.Framework == "external" {
		return []string{"No call graph available for external endpoints."}, nil
	}
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
	if g == nil || ep.HandlerFunc == "" {
		return nil
	}

	// 1. Direct ID match (e.g. n.ID == "handlers.JoinQueue")
	if n, ok := g.Nodes[ep.HandlerFunc]; ok && n.Type == graph.FunctionNode {
		return n
	}
	for _, n := range g.Nodes {
		if n.Type != graph.FunctionNode {
			continue
		}
		if n.ID == ep.HandlerFunc {
			return n
		}
	}

	// 2. If handler is qualified like "handlers.JoinQueue"
	if strings.Contains(ep.HandlerFunc, ".") {
		parts := strings.Split(ep.HandlerFunc, ".")
		targetFunc := parts[len(parts)-1]
		qualifier := parts[len(parts)-2]

		var qualifiedCandidates []*graph.Node
		for _, n := range g.Nodes {
			if n.Type != graph.FunctionNode {
				continue
			}
			if n.Name == targetFunc {
				// Check if ID contains qualifier or path contains qualifier directory
				slashPath := filepath.ToSlash(n.Path)
				if strings.Contains(n.ID, qualifier) || strings.Contains(slashPath, "/"+qualifier+"/") || strings.HasPrefix(slashPath, qualifier+"/") {
					qualifiedCandidates = append(qualifiedCandidates, n)
				}
			}
		}
		if len(qualifiedCandidates) == 1 {
			return qualifiedCandidates[0]
		}
		if len(qualifiedCandidates) > 1 {
			for _, n := range qualifiedCandidates {
				if filepath.Base(filepath.Dir(n.Path)) == qualifier {
					return n
				}
			}
			return qualifiedCandidates[0]
		}
	}

	// 3. Match in the same file (e.g. Python decorator or local Go function)
	var fileCandidates []*graph.Node
	targetName := ep.HandlerFunc
	if idx := strings.LastIndex(targetName, "."); idx != -1 {
		targetName = targetName[idx+1:]
	}

	for _, n := range g.Nodes {
		if n.Type != graph.FunctionNode {
			continue
		}
		if (strings.HasSuffix(n.Path, ep.File) || strings.HasSuffix(ep.File, n.Path)) && n.Name == targetName {
			fileCandidates = append(fileCandidates, n)
		}
	}
	if len(fileCandidates) > 0 {
		var best *graph.Node
		bestDist := 999999
		for _, n := range fileCandidates {
			dist := n.Line - ep.Line
			if dist >= 0 && dist < bestDist {
				best = n
				bestDist = dist
			}
		}
		if best != nil {
			return best
		}
		return fileCandidates[0]
	}

	// 4. Match in the same directory (same package across multiple files)
	epDir := filepath.Dir(ep.File)
	for _, n := range g.Nodes {
		if n.Type != graph.FunctionNode {
			continue
		}
		if filepath.Dir(n.Path) == epDir && n.Name == targetName {
			return n
		}
	}

	// 5. Global fallback by function name
	for _, n := range g.Nodes {
		if n.Type != graph.FunctionNode {
			continue
		}
		if n.Name == targetName {
			return n
		}
	}

	return nil
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

func deduplicateEndpoints(endpoints []frameworks.Endpoint) []frameworks.Endpoint {
	seen := make(map[string]bool)
	var unique []frameworks.Endpoint
	for _, ep := range endpoints {
		key := strings.ToUpper(ep.Method) + ":" + ep.Path
		if !seen[key] {
			seen[key] = true
			unique = append(unique, ep)
		}
	}
	return unique
}

// entry point
func StartAPI(endpoints []frameworks.Endpoint, config APIConfig, g *graph.Graph, startGlobal bool) error {
	endpoints = deduplicateEndpoints(endpoints)
	extEndpoints := loadExternalEndpoints()
	activeSec := 0
	rTab := TabCallGraph
	if startGlobal || len(endpoints) == 0 {
		activeSec = 1
		rTab = TabResponse
	}
	model := APIModel{
		endpoints:         endpoints,
		filtered:          endpoints,
		externalEndpoints: extEndpoints,
		filteredExternal:  extEndpoints,
		activeSection:     activeSec,
		apiConfig:         config,
		screen:            ListScreen,
		graph:             g,
		rightTab:          rTab,
	}
	model.bodyInput = textarea.New()
	model.fileFieldInput = textinput.New()
	model.filePathInput = textinput.New()
	model.bodyKind = BodyJSON
	model.refreshCaches()

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
			if model.tempFileToOpen != "" {
				openAPIEditor(&LocationToOpen{Path: model.tempFileToOpen})
				if model.tempFileIsBody {
					if data, err := os.ReadFile(model.tempFileToOpen); err == nil {
						model.bodyInput.SetValue(string(data))
					}
				}
				_ = os.Remove(model.tempFileToOpen)
				model.tempFileToOpen = ""
				model.tempFileIsBody = false
				continue
			}
		}
		return nil
	}
}
