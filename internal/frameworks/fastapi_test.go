package frameworks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFastAPIRouteDetection(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fastapi-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pyCode := `
from fastapi import FastAPI, APIRouter

app = FastAPI()
router = APIRouter(prefix="/api/v1")

@app.get("/index")
def index():
    return "home"

@app.get("/users/{user_id}")
def get_user(user_id: int):
    return "user"

@router.api_route("/tracks", methods=["GET", "POST"])
def tracks():
    return "tracks"

@router.post("/tracks/{track_id}")
def create_track(track_id: str):
    return "track"
`
	tmpFile := filepath.Join(tmpDir, "app.py")
	if err := os.WriteFile(tmpFile, []byte(pyCode), 0644); err != nil {
		t.Fatalf("failed to write test python file: %v", err)
	}

	if err := os.MkdirAll("queries", 0755); err != nil {
		t.Fatalf("failed to create queries dir: %v", err)
	}
	qData, err := os.ReadFile("../../queries/fastapi.scm")
	if err != nil {
		t.Fatalf("failed to read source query file: %v", err)
	}
	if err := os.WriteFile("queries/fastapi.scm", qData, 0644); err != nil {
		t.Fatalf("failed to write package queries/fastapi.scm: %v", err)
	}
	defer os.RemoveAll("queries")

	endpoints, err := DetectFastAPIEndpoints([]string{tmpFile})
	if err != nil {
		t.Fatalf("endpoint detection failed: %v", err)
	}

	expected := []Endpoint{
		{Method: "GET", Path: "/index", HandlerFunc: "index"},
		{Method: "GET", Path: "/users/<user_id>", HandlerFunc: "get_user", PathParams: []PathParam{{Name: "user_id", Type: "string"}}},
		{Method: "GET", Path: "/api/v1/tracks", HandlerFunc: "tracks"},
		{Method: "POST", Path: "/api/v1/tracks", HandlerFunc: "tracks"},
		{Method: "POST", Path: "/api/v1/tracks/<track_id>", HandlerFunc: "create_track", PathParams: []PathParam{{Name: "track_id", Type: "string"}}},
	}

	if len(endpoints) != len(expected) {
		t.Fatalf("expected %d endpoints, got %d: %+v", len(expected), len(endpoints), endpoints)
	}

	for i, exp := range expected {
		actual := endpoints[i]
		if actual.Method != exp.Method {
			t.Errorf("[%d] expected method %s, got %s", i, exp.Method, actual.Method)
		}
		if actual.Path != exp.Path {
			t.Errorf("[%d] expected path %s, got %s", i, exp.Path, actual.Path)
		}
		if actual.HandlerFunc != exp.HandlerFunc {
			t.Errorf("[%d] expected handler %s, got %s", i, exp.HandlerFunc, actual.HandlerFunc)
		}
		if len(actual.PathParams) != len(exp.PathParams) {
			t.Errorf("[%d] expected %d params, got %d", i, len(exp.PathParams), len(actual.PathParams))
			continue
		}
		for j, p := range exp.PathParams {
			actP := actual.PathParams[j]
			if actP.Name != p.Name || actP.Type != p.Type {
				t.Errorf("[%d][%d] expected param %+v, got %+v", i, j, p, actP)
			}
		}
	}
}
