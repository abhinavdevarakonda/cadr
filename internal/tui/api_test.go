package tui

import (
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abhinavdevarakonda/cadr/internal/frameworks"
	tea "github.com/charmbracelet/bubbletea"
)

func runRequestSpec(t *testing.T, spec requestSpec) responseMsg {
	t.Helper()
	msg := sendRequest(spec)()
	rm, ok := msg.(responseMsg)
	if !ok {
		t.Fatalf("expected responseMsg, got %T", msg)
	}
	return rm
}

func TestParseHeaderLinesKeepsCommasInValues(t *testing.T) {
	headers := parseHeaderLines("Accept: text/html, application/json\nX-Trace: 1")

	if len(headers) != 2 {
		t.Fatalf("expected 2 headers, got %d: %+v", len(headers), headers)
	}
	if headers[0][0] != "Accept" || headers[0][1] != "text/html, application/json" {
		t.Errorf("Accept header was split: %+v", headers[0])
	}
	if headers[1][0] != "X-Trace" || headers[1][1] != "1" {
		t.Errorf("unexpected second header: %+v", headers[1])
	}
}

func TestParseHeaderLinesAcceptsCommaSeparatedHeaders(t *testing.T) {
	headers := parseHeaderLines("Accept: text/html, application/json, X-Trace: 1")

	if len(headers) != 2 {
		t.Fatalf("expected 2 headers, got %d: %+v", len(headers), headers)
	}
	if headers[0][1] != "text/html, application/json" {
		t.Errorf("value comma was not preserved: %+v", headers[0])
	}
	if headers[1][0] != "X-Trace" || headers[1][1] != "1" {
		t.Errorf("comma-separated header not parsed: %+v", headers[1])
	}
}

func TestSendRequestSetsJSONContentTypeByDefault(t *testing.T) {
	var gotContentType, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	rm := runRequestSpec(t, requestSpec{
		Method:   "POST",
		URL:      server.URL,
		BodyKind: BodyJSON,
		Body:     `{"a":1}`,
		Timeout:  time.Second,
	})
	if rm.err != nil {
		t.Fatalf("unexpected error: %v", rm.err)
	}
	if gotContentType != "application/json" {
		t.Errorf("expected application/json, got %q", gotContentType)
	}
	if gotBody != `{"a":1}` {
		t.Errorf("unexpected body %q", gotBody)
	}
}

func TestSendRequestKeepsUserContentType(t *testing.T) {
	var gotContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	rm := runRequestSpec(t, requestSpec{
		Method:   "POST",
		URL:      server.URL,
		Headers:  "Content-Type: application/vnd.api+json",
		BodyKind: BodyJSON,
		Body:     `{"a":1}`,
		Timeout:  time.Second,
	})
	if rm.err != nil {
		t.Fatalf("unexpected error: %v", rm.err)
	}
	if gotContentType != "application/vnd.api+json" {
		t.Errorf("user Content-Type was overwritten: %q", gotContentType)
	}
}

func TestSendRequestEncodesFormBody(t *testing.T) {
	var gotContentType, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	rm := runRequestSpec(t, requestSpec{
		Method:   "POST",
		URL:      server.URL,
		BodyKind: BodyForm,
		Body:     "name=hello world\nage=42\n",
		Timeout:  time.Second,
	})
	if rm.err != nil {
		t.Fatalf("unexpected error: %v", rm.err)
	}
	if gotContentType != "application/x-www-form-urlencoded" {
		t.Errorf("unexpected Content-Type %q", gotContentType)
	}
	if gotBody != "age=42&name=hello+world" {
		t.Errorf("unexpected encoded body %q", gotBody)
	}
}

func TestSendRequestMultipartWithFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "photo.txt")
	if err := os.WriteFile(filePath, []byte("file-bytes"), 0644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	var gotField, gotFile string
	var gotFileName string
	var hadMultipart bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("expected multipart body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		hadMultipart = true
		gotField = r.FormValue("label")
		file, header, err := r.FormFile("photo")
		if err == nil {
			defer file.Close()
			data, _ := io.ReadAll(file)
			gotFile = string(data)
			gotFileName = header.Filename
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	rm := runRequestSpec(t, requestSpec{
		Method:    "POST",
		URL:       server.URL,
		BodyKind:  BodyMultipart,
		Body:      "label=avatar",
		FileField: "photo",
		FilePath:  filePath,
		Timeout:   time.Second,
	})
	if rm.err != nil {
		t.Fatalf("unexpected error: %v", rm.err)
	}
	if !hadMultipart {
		t.Fatal("server did not receive a multipart form")
	}
	if gotField != "avatar" {
		t.Errorf("expected text field label=avatar, got %q", gotField)
	}
	if gotFile != "file-bytes" {
		t.Errorf("expected file contents, got %q", gotFile)
	}
	if gotFileName != "photo.txt" {
		t.Errorf("expected filename photo.txt, got %q", gotFileName)
	}
}

func TestSendRequestMultipartFieldsOnly(t *testing.T) {
	var gotLabel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("expected multipart body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotLabel = r.FormValue("label")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	rm := runRequestSpec(t, requestSpec{
		Method:   "POST",
		URL:      server.URL,
		BodyKind: BodyMultipart,
		Body:     "label=only-fields",
		Timeout:  time.Second,
	})
	if rm.err != nil {
		t.Fatalf("unexpected error: %v", rm.err)
	}
	if gotLabel != "only-fields" {
		t.Errorf("expected label=only-fields, got %q", gotLabel)
	}
}

func TestSendRequestMultipartFileErrors(t *testing.T) {
	rm := runRequestSpec(t, requestSpec{
		Method:    "POST",
		URL:       "http://127.0.0.1:1/upload",
		BodyKind:  BodyMultipart,
		FileField: "file",
		FilePath:  filepath.Join(t.TempDir(), "missing.bin"),
		Timeout:   time.Second,
	})
	if rm.err == nil {
		t.Fatal("expected an error for an unreadable file")
	}
	if !strings.Contains(rm.err.Error(), "cannot read") {
		t.Errorf("expected a readable file error, got %v", rm.err)
	}
}

func TestSendRequestMultipartRequiresFileField(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	rm := runRequestSpec(t, requestSpec{
		Method:   "POST",
		URL:      "http://127.0.0.1:1/upload",
		BodyKind: BodyMultipart,
		FilePath: filePath,
		Timeout:  time.Second,
	})
	if rm.err == nil {
		t.Fatal("expected an error when the file field is blank")
	}
	if !strings.Contains(rm.err.Error(), "file field") {
		t.Errorf("expected a file field error, got %v", rm.err)
	}
}

func TestSendRequestTimeoutReportsAPITimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	rm := runRequestSpec(t, requestSpec{
		Method:  "GET",
		URL:     server.URL,
		Timeout: 30 * time.Millisecond,
	})
	if rm.err == nil {
		t.Fatal("expected a timeout error")
	}
	if !rm.timedOut {
		t.Errorf("expected timedOut to be set, got %v", rm.err)
	}

	model := APIModel{
		endpoints: []frameworks.Endpoint{{Method: "GET", Path: "/slow", Framework: "gin"}},
		filtered:  []frameworks.Endpoint{{Method: "GET", Path: "/slow", Framework: "gin"}},
		apiConfig: APIConfig{GinURL: server.URL, RequestTimeout: 30 * time.Millisecond},
	}
	updated, _ := model.Update(rm)
	got, ok := updated.(APIModel)
	if !ok {
		t.Fatalf("expected APIModel, got %T", updated)
	}
	if got.statusCode != 0 {
		t.Errorf("network failure must not fake an HTTP status, got %d", got.statusCode)
	}
	if !strings.Contains(got.responseErr, "api_timeout") {
		t.Errorf("expected timeout message to name api_timeout, got %q", got.responseErr)
	}
}

func TestNetworkErrorDoesNotFakeStatus(t *testing.T) {
	rm := runRequestSpec(t, requestSpec{
		Method:  "GET",
		URL:     "http://127.0.0.1:1/nothing-listens-here",
		Timeout: 200 * time.Millisecond,
	})
	if rm.err == nil {
		t.Fatal("expected a connection error")
	}

	model := APIModel{
		endpoints: []frameworks.Endpoint{{Method: "GET", Path: "/x", Framework: "gin"}},
		filtered:  []frameworks.Endpoint{{Method: "GET", Path: "/x", Framework: "gin"}},
		apiConfig: APIConfig{GinURL: "http://127.0.0.1:1"},
	}
	updated, _ := model.Update(rm)
	got := updated.(APIModel)
	if got.statusCode != 0 {
		t.Errorf("expected status 0 on a connection error, got %d", got.statusCode)
	}
	if got.responseErr == "" {
		t.Error("expected an error state to be set")
	}
}

func TestSubstitutePathParamsKeepsNonPlaceholders(t *testing.T) {
	path := "/users/<int:user_id>/files/:name"
	params := []frameworks.PathParam{{Name: "user_id", Type: "int"}, {Name: "name", Type: "string"}}

	got := substitutePathParams(path, params, []string{"7", "report"})
	if got != "/users/7/files/report" {
		t.Fatalf("unexpected substitution: %q", got)
	}

	got = substitutePathParams(path, params, []string{"", ""})
	if got != path {
		t.Fatalf("blank values should leave the path alone, got %q", got)
	}
}

func TestResolveTargetURLNode(t *testing.T) {
	model := APIModel{apiConfig: APIConfig{
		DefaultURL: "http://localhost:5000",
		NodeURL:    "http://localhost:3001",
	}}

	got := model.resolveTargetURL(frameworks.Endpoint{Framework: "node"})
	if got != "http://localhost:3001" {
		t.Fatalf("expected node_api_url, got %q", got)
	}
}

func TestMethodSupportsBody(t *testing.T) {
	cases := map[string]bool{
		"GET":     false,
		"HEAD":    false,
		"POST":    true,
		"PUT":     true,
		"PATCH":   true,
		"DELETE":  true,
		"OPTIONS": true,
	}
	for method, want := range cases {
		model := APIModel{
			filtered: []frameworks.Endpoint{{Method: method, Path: "/x"}},
		}
		if got := model.methodSupportsBody(); got != want {
			t.Errorf("%s: expected %v, got %v", method, want, got)
		}
	}
}

func TestBodyKindCycle(t *testing.T) {
	model := APIModel{
		filtered: []frameworks.Endpoint{{Method: "POST", Path: "/x"}},
		bodyKind: BodyJSON,
	}

	got := []BodyKind{}
	for i := 0; i < 4; i++ {
		model.cycleBodyKind()
		got = append(got, model.bodyKind.normalized())
	}

	want := []BodyKind{BodyForm, BodyMultipart, BodyRaw, BodyJSON}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cycle %d: expected %s, got %s (all: %+v)", i, want[i], got[i], got)
		}
	}
}

func TestFocusInputWalksMultipartRows(t *testing.T) {
	model := APIModel{
		filtered:   []frameworks.Endpoint{{Method: "POST", Path: "/users/<int:id>", PathParams: []frameworks.PathParam{{Name: "id", Type: "int"}}}},
		pathInputs: nil,
	}
	model.initForm()
	model.bodyKind = BodyMultipart

	// 1 path param + query + headers + body + file field + file path
	if got := model.fieldCount(); got != 6 {
		t.Fatalf("expected 6 focusable fields, got %d", got)
	}
	if model.fileFieldIdx() != 4 || model.filePathIdx() != 5 {
		t.Fatalf("unexpected file indices: %d %d", model.fileFieldIdx(), model.filePathIdx())
	}

	model.focusInput(-1)
	if model.focusIdx != 5 {
		t.Errorf("backwards navigation should wrap to the last field, got %d", model.focusIdx)
	}
	model.focusInput(6)
	if model.focusIdx != 0 {
		t.Errorf("forward navigation should wrap to the first field, got %d", model.focusIdx)
	}

	model.bodyKind = BodyJSON
	if got := model.fieldCount(); got != 4 {
		t.Errorf("expected 4 fields in json mode, got %d", got)
	}
	if model.fileFieldIdx() != -1 || model.filePathIdx() != -1 {
		t.Errorf("file rows must not exist outside multipart mode")
	}
}

func TestSavedBodyModeIsRestored(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cache := APICache{
		endpointKey("POST", "/upload"): {
			BodyKind:  BodyMultipart,
			Body:      "label=avatar",
			FileField: "photo",
			FilePath:  "./photo.png",
		},
	}
	saveCache(cache)

	model := APIModel{
		endpoints: []frameworks.Endpoint{{Method: "POST", Path: "/upload", Framework: "gin"}},
		filtered:  []frameworks.Endpoint{{Method: "POST", Path: "/upload", Framework: "gin"}},
		apiConfig: APIConfig{GinURL: "http://localhost:8080"},
		width:     120,
	}
	model.refreshCaches()
	model.initForm()

	if model.bodyKind != BodyMultipart {
		t.Fatalf("expected multipart to be restored, got %q", model.bodyKind)
	}
	if model.fileFieldInput.Value() != "photo" {
		t.Errorf("expected file field photo, got %q", model.fileFieldInput.Value())
	}
	if model.filePathInput.Value() != "./photo.png" {
		t.Errorf("expected file path ./photo.png, got %q", model.filePathInput.Value())
	}
	if model.bodyInput.Value() != "label=avatar" {
		t.Errorf("expected saved fields, got %q", model.bodyInput.Value())
	}
}

func TestLegacyCacheWithoutBodyKindDefaultsToJSON(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(".cadr", "cache"), 0755); err != nil {
		t.Fatalf("failed to create cache dir: %v", err)
	}
	legacy := `{"GET:/items":{"path_params":[],"query_params":"","headers":"","body":"","hit_count":1,"last_called":""}}`
	if err := os.WriteFile(filepath.Join(".cadr", "cache", "api_cache.json"), []byte(legacy), 0644); err != nil {
		t.Fatalf("failed to write legacy cache: %v", err)
	}

	model := APIModel{
		endpoints: []frameworks.Endpoint{{Method: "POST", Path: "/items", Framework: "gin"}},
		filtered:  []frameworks.Endpoint{{Method: "POST", Path: "/items", Framework: "gin"}},
		width:     120,
	}
	model.refreshCaches()
	model.initForm()

	if model.bodyKind != BodyJSON {
		t.Fatalf("expected json default for old caches, got %q", model.bodyKind)
	}
}

func TestGetAndHeadHideBodyRow(t *testing.T) {
	model := APIModel{
		filtered: []frameworks.Endpoint{{Method: "GET", Path: "/x"}},
	}
	model.initForm()

	if model.bodyFieldIdx() != -1 {
		t.Errorf("GET must not expose a body row")
	}
	if got := model.fieldCount(); got != 2 {
		t.Errorf("expected query+headers only, got %d fields", got)
	}
}

func TestMultipartBoundaryIsContentType(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(filePath, []byte("abc"), 0644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	reader, contentType, err := buildRequestBody(requestSpec{
		BodyKind:  BodyMultipart,
		Body:      "a=1",
		FileField: "file",
		FilePath:  filePath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(contentType, "multipart/form-data; boundary=") {
		t.Fatalf("unexpected Content-Type %q", contentType)
	}

	// The body must round-trip through a multipart reader.
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	mr := multipart.NewReader(strings.NewReader(string(data)), strings.TrimPrefix(contentType, "multipart/form-data; boundary="))
	if _, err := mr.ReadForm(1 << 20); err != nil {
		t.Fatalf("body was not valid multipart: %v", err)
	}
}

func TestBKeyCyclesBodyModeInForm(t *testing.T) {
	model := APIModel{
		filtered: []frameworks.Endpoint{{Method: "POST", Path: "/upload", Framework: "gin"}},
		width:    120,
		height:   40,
	}
	model.screen = FormScreen
	model.initForm()

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	got := updated.(APIModel)
	if got.bodyKind != BodyForm {
		t.Fatalf("expected b to move json → form, got %q", got.bodyKind)
	}
}

func TestFormViewShowsMultipartRows(t *testing.T) {
	model := APIModel{
		filtered: []frameworks.Endpoint{{Method: "POST", Path: "/upload", Framework: "gin"}},
		width:    120,
		height:   40,
	}
	model.screen = FormScreen
	model.initForm()
	model.bodyKind = BodyMultipart

	view := model.View()
	for _, want := range []string{"Body (multipart)", "File field:", "File path"} {
		if !strings.Contains(view, want) {
			t.Errorf("multipart form view is missing %q", want)
		}
	}
}

func TestFormViewHidesBodyForGET(t *testing.T) {
	model := APIModel{
		filtered: []frameworks.Endpoint{{Method: "GET", Path: "/items", Framework: "gin"}},
		width:    120,
		height:   40,
	}
	model.screen = FormScreen
	model.initForm()

	view := model.View()
	if strings.Contains(view, "Body (") {
		t.Errorf("GET form view must not render a body section:\n%s", view)
	}
	if !strings.Contains(view, "Headers:") {
		t.Error("GET form view should still render the headers field")
	}
}

func TestResponseViewRendersErrorState(t *testing.T) {
	model := APIModel{
		endpoints: []frameworks.Endpoint{{Method: "GET", Path: "/x", Framework: "gin"}},
		filtered:  []frameworks.Endpoint{{Method: "GET", Path: "/x", Framework: "gin"}},
		apiConfig: APIConfig{GinURL: "http://localhost:8080"},
		width:     120,
		height:    40,
	}
	model.responseErr = "timed out after 10s — raise api_timeout in .cadr/config.yaml"

	view := model.View()
	if !strings.Contains(view, "request failed") {
		t.Errorf("error state did not render:\n%s", view)
	}
	if !strings.Contains(view, "api_timeout") {
		t.Errorf("error state lost the api_timeout hint:\n%s", view)
	}
}

func TestFormSendUsesSelectedBodyModeAndPersistsIt(t *testing.T) {
	t.Chdir(t.TempDir())

	filePath := filepath.Join(t.TempDir(), "avatar.txt")
	if err := os.WriteFile(filePath, []byte("avatar-bytes"), 0644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	var gotLabel, gotFile, gotTrace string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTrace = r.Header.Get("X-Trace")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("expected multipart body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotLabel = r.FormValue("label")
		if file, _, err := r.FormFile("photo"); err == nil {
			defer file.Close()
			data, _ := io.ReadAll(file)
			gotFile = string(data)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	ep := frameworks.Endpoint{Method: "POST", Path: "/upload", Framework: "gin"}
	model := APIModel{
		endpoints: []frameworks.Endpoint{ep},
		filtered:  []frameworks.Endpoint{ep},
		apiConfig: APIConfig{GinURL: server.URL, RequestTimeout: 2 * time.Second},
		width:     120,
		height:    40,
	}
	model.refreshCaches()
	model.screen = FormScreen
	model.initForm()

	model.bodyKind = BodyMultipart
	model.bodyInput.SetValue("label=avatar")
	model.fileFieldInput.SetValue("photo")
	model.filePathInput.SetValue(filePath)
	model.headerInput.SetValue("X-Trace: 1")

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := updated.(APIModel)
	if cmd == nil {
		t.Fatal("ctrl+s did not return a request command")
	}

	msg := cmd()
	rm, ok := msg.(responseMsg)
	if !ok {
		t.Fatalf("expected responseMsg, got %T", msg)
	}
	if rm.err != nil {
		t.Fatalf("request failed: %v", rm.err)
	}
	if rm.statusCode != http.StatusCreated {
		t.Errorf("expected 201, got %d", rm.statusCode)
	}
	if gotLabel != "avatar" || gotFile != "avatar-bytes" {
		t.Errorf("multipart body lost data: label=%q file=%q", gotLabel, gotFile)
	}
	if gotTrace != "1" {
		t.Errorf("header field was not sent: X-Trace=%q", gotTrace)
	}

	saved := got.cache[endpointKey("POST", "/upload")]
	if saved.BodyKind != BodyMultipart {
		t.Errorf("expected the body mode to be persisted, got %q", saved.BodyKind)
	}
	if saved.FileField != "photo" || saved.FilePath != filePath {
		t.Errorf("expected multipart file fields to be persisted, got %+v", saved)
	}
	if saved.HitCount != 1 {
		t.Errorf("expected hit count 1, got %d", saved.HitCount)
	}
}

func TestSubstituteUnresolvedPathParams(t *testing.T) {
	params := []frameworks.PathParam{{Name: "API_PREFIX", Type: frameworks.UnresolvedPathParamType}}

	if got := substitutePathParams("/[API_PREFIX]/books", params, []string{"/api/v1"}); got != "/api/v1/books" {
		t.Errorf("expected a single slash at the boundary, got %q", got)
	}
	if got := substitutePathParams("/[API_PREFIX]/books", params, []string{"api/v1"}); got != "/api/v1/books" {
		t.Errorf("expected the leading slash to be kept, got %q", got)
	}
	if got := substitutePathParams("/[API_PREFIX]/books", params, []string{""}); got != "/[API_PREFIX]/books" {
		t.Errorf("expected the token to stay untouched when blank, got %q", got)
	}
}

func TestInitFormMarksUnresolvedParams(t *testing.T) {
	ep := frameworks.Endpoint{
		Method: "GET",
		Path:   "/[API_PREFIX]/books",
		PathParams: []frameworks.PathParam{
			{Name: "API_PREFIX", Type: frameworks.UnresolvedPathParamType},
			{Name: "id", Type: "string"},
		},
	}
	model := APIModel{
		endpoints:     []frameworks.Endpoint{ep},
		filtered:      []frameworks.Endpoint{ep},
		cache:         APICache{},
		externalCache: map[string]SavedRequest{},
	}
	model.initForm()

	if len(model.pathInputs) != 2 {
		t.Fatalf("expected 2 path inputs, got %d", len(model.pathInputs))
	}
	if got := model.pathInputs[0].Placeholder; got != "unresolved in source — e.g. /api/v1" {
		t.Errorf("unexpected unresolved placeholder %q", got)
	}
	if got := model.pathInputs[1].Placeholder; got != "type: string" {
		t.Errorf("unexpected resolved placeholder %q", got)
	}
}
