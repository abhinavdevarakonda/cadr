package frameworks

import (
	"strings"
	"testing"
)

func TestFlaskResolvesRelativeImportConstant(t *testing.T) {
	paths := writeNodeProject(t, map[string]string{
		"settings.py": "API_PREFIX = '/api/v1'\n",
		"app.py": `from flask import Flask
from .settings import API_PREFIX

app = Flask(__name__)


@app.route(API_PREFIX + "/books")
def books():
    return "books"
`,
	})

	endpoints, err := DetectFlaskEndpoints(paths)
	if err != nil {
		t.Fatalf("flask detection failed: %v", err)
	}
	ep := requireRoute(t, endpoints, "GET /api/v1/books")
	if len(ep.PathParams) != 0 {
		t.Errorf("expected no unresolved params, got %+v", ep.PathParams)
	}
}

func TestFastAPIResolvesRenamedImportAndFString(t *testing.T) {
	paths := writeNodeProject(t, map[string]string{
		"pkg/settings.py": "API_PREFIX = '/api/v2'\n",
		"pkg/routes.py": `from fastapi import FastAPI
from pkg.settings import API_PREFIX as P

app = FastAPI()


@app.get(f"{P}/books")
def books():
    return "books"
`,
	})

	endpoints, err := DetectFastAPIEndpoints(paths)
	if err != nil {
		t.Fatalf("fastapi detection failed: %v", err)
	}
	requireRoute(t, endpoints, "GET /api/v2/books")
}

func TestFastAPIRouterPrefixConstant(t *testing.T) {
	paths := writeNodeProject(t, map[string]string{
		"settings.py": "API_PREFIX = '/api/v3'\n",
		"app.py": `from fastapi import APIRouter, FastAPI
from settings import API_PREFIX

app = FastAPI()
router = APIRouter(prefix=API_PREFIX)


@router.get("/books")
def books():
    return "books"
`,
	})

	endpoints, err := DetectFastAPIEndpoints(paths)
	if err != nil {
		t.Fatalf("fastapi detection failed: %v", err)
	}
	requireRoute(t, endpoints, "GET /api/v3/books")
}

func TestFastAPIIncludeRouterPrefixConstant(t *testing.T) {
	paths := writeNodeProject(t, map[string]string{
		"settings.py": "API_PREFIX = '/api/v6'\n",
		"app.py": `from fastapi import APIRouter, FastAPI
from settings import API_PREFIX

app = FastAPI()
api = APIRouter()


@api.get("/books")
def books():
    return "books"


app.include_router(api, prefix=API_PREFIX)
`,
	})

	endpoints, err := DetectFastAPIEndpoints(paths)
	if err != nil {
		t.Fatalf("fastapi detection failed: %v", err)
	}
	requireRoute(t, endpoints, "GET /api/v6/books")
}

func TestFlaskBlueprintPrefixConstant(t *testing.T) {
	paths := writeNodeProject(t, map[string]string{
		"settings.py": "API_PREFIX = '/api/v4'\n",
		"app.py": `from flask import Blueprint, Flask
from settings import API_PREFIX

app = Flask(__name__)
bp = Blueprint("bp", __name__, url_prefix=API_PREFIX)


@bp.route("/books")
def books():
    return "books"
`,
	})

	endpoints, err := DetectFlaskEndpoints(paths)
	if err != nil {
		t.Fatalf("flask detection failed: %v", err)
	}
	requireRoute(t, endpoints, "GET /api/v4/books")
}

func TestFlaskResolvesGetenvDefault(t *testing.T) {
	paths := writeNodeProject(t, map[string]string{
		"app.py": `import os

from flask import Flask

app = Flask(__name__)
API_PREFIX = os.getenv("API_PREFIX", "/api/v5")


@app.route(API_PREFIX + "/books")
def books():
    return "books"
`,
	})

	endpoints, err := DetectFlaskEndpoints(paths)
	if err != nil {
		t.Fatalf("flask detection failed: %v", err)
	}
	requireRoute(t, endpoints, "GET /api/v5/books")
}

func TestFlaskUnresolvedPathBecomesFillable(t *testing.T) {
	paths := writeNodeProject(t, map[string]string{
		"app.py": `import os

from flask import Flask

app = Flask(__name__)


@app.route(os.getenv("BOOKS_PATH"))
def books():
    return "books"
`,
	})

	endpoints, err := DetectFlaskEndpoints(paths)
	if err != nil {
		t.Fatalf("flask detection failed: %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %+v", endpoints)
	}
	ep := endpoints[0]
	if len(ep.PathParams) != 1 || ep.PathParams[0].Type != UnresolvedPathParamType {
		t.Fatalf("expected an unresolved param, got %+v", ep.PathParams)
	}
	if !strings.Contains(ep.Path, "exprL") {
		t.Errorf("expected a placeholder token in the path, got %q", ep.Path)
	}
}

func TestFastAPIUnresolvedRouterPrefixBecomesFillable(t *testing.T) {
	paths := writeNodeProject(t, map[string]string{
		"app.py": `import os

from fastapi import APIRouter, FastAPI

app = FastAPI()
router = APIRouter(prefix=os.environ.get("API_PREFIX"))


@router.get("/books")
def books():
    return "books"
`,
	})

	endpoints, err := DetectFastAPIEndpoints(paths)
	if err != nil {
		t.Fatalf("fastapi detection failed: %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %+v", endpoints)
	}
	ep := endpoints[0]
	if !strings.Contains(ep.Path, "exprL") || !strings.HasSuffix(ep.Path, "/books") {
		t.Errorf("expected a fillable prefix, got %q", ep.Path)
	}
	if len(ep.PathParams) != 1 || ep.PathParams[0].Type != UnresolvedPathParamType {
		t.Errorf("expected an unresolved param, got %+v", ep.PathParams)
	}
}

func TestPythonFileWithoutImportsResolvesLiterals(t *testing.T) {
	paths := writeNodeProject(t, map[string]string{
		"app.py": `from flask import Flask

app = Flask(__name__)


@app.route("/plain")
def plain():
    return "plain"
`,
	})

	endpoints, err := DetectFlaskEndpoints(paths)
	if err != nil {
		t.Fatalf("flask detection failed: %v", err)
	}
	requireRoute(t, endpoints, "GET /plain")
}

func TestPyIndexResolvesModulesWithRelativePaths(t *testing.T) {
	// `cadr api list --local` scans with project-relative paths; the FastAPI
	// include graph has to resolve those too, not just absolute ones.
	idx := &pyIndex{files: map[string]*pyModule{
		"app/http/routes_books.py": {path: "app/http/routes_books.py"},
	}}
	got, ok := idx.resolveModule("app/main.py", "app.http.routes_books")
	if !ok || got != "app/http/routes_books.py" {
		t.Fatalf("expected the relative module path, got %q (ok=%v)", got, ok)
	}
}

// The reported bug: the router lives in another module and its prefix comes
// from a dataclass field default (`settings.api_base`), with empty decorator
// paths that must not become prefix + "/".
func TestFastAPIResolvesCrossFileIncludePrefix(t *testing.T) {
	paths := writeNodeProject(t, map[string]string{
		"app/settings.py": `from dataclasses import dataclass

DEFAULT_API_BASE = "/api/v1"


@dataclass(frozen=True)
class Settings:
    api_base: str = DEFAULT_API_BASE
`,
		"app/http/routes_books.py": `from fastapi import APIRouter

router = APIRouter()


@router.get("")
def list_books():
    return "books"


@router.get("/search")
def search_books():
    return "books"
`,
		"app/main.py": `from fastapi import FastAPI

from app.http import routes_books
from app.settings import Settings


def create_app(settings: Settings) -> FastAPI:
    app = FastAPI()
    app.include_router(routes_books.router, prefix=settings.api_base + "/books")
    return app
`,
	})

	endpoints, err := DetectFastAPIEndpoints(paths)
	if err != nil {
		t.Fatalf("fastapi detection failed: %v", err)
	}
	if len(endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %+v", endpoints)
	}
	for _, ep := range endpoints {
		if ep.Path == "" {
			t.Errorf("empty endpoint path on %s", ep.HandlerFunc)
		}
		if len(ep.PathParams) != 0 {
			t.Errorf("expected no unresolved params on %s %s, got %+v", ep.Method, ep.Path, ep.PathParams)
		}
	}
	requireRoute(t, endpoints, "GET /api/v1/books")
	requireRoute(t, endpoints, "GET /api/v1/books/search")
}

func TestFastAPIUnresolvedIncludePrefixBecomesFillable(t *testing.T) {
	paths := writeNodeProject(t, map[string]string{
		"app/settings.py": `ONE = "/one"
TWO = "/two"


class First:
    api_base: str = ONE


class Second:
    api_base: str = TWO
`,
		"app/http/routes_books.py": `from fastapi import APIRouter

router = APIRouter()


@router.get("")
def list_books():
    return "books"
`,
		"app/main.py": `from fastapi import FastAPI

from app.http import routes_books
from app.settings import Settings


def create_app(settings: Settings) -> FastAPI:
    app = FastAPI()
    app.include_router(routes_books.router, prefix=settings.api_base + "/books")
    return app
`,
	})

	endpoints, err := DetectFastAPIEndpoints(paths)
	if err != nil {
		t.Fatalf("fastapi detection failed: %v", err)
	}
	ep := requireRoute(t, endpoints, "GET /[settings.api_base]/books")
	if len(ep.PathParams) != 1 || ep.PathParams[0].Name != "settings.api_base" || ep.PathParams[0].Type != UnresolvedPathParamType {
		t.Errorf("expected one fillable settings.api_base param, got %+v", ep.PathParams)
	}
}

func TestFastAPIDoubleIncludedRouterIsFillable(t *testing.T) {
	paths := writeNodeProject(t, map[string]string{
		"app/http/routes_books.py": `from fastapi import APIRouter

router = APIRouter()


@router.get("/search")
def search_books():
    return "books"
`,
		"app/main.py": `from fastapi import FastAPI

from app.http import routes_books


def create_app() -> FastAPI:
    app = FastAPI()
    app.include_router(routes_books.router, prefix="/v1")
    app.include_router(routes_books.router, prefix="/v2")
    return app
`,
	})

	endpoints, err := DetectFastAPIEndpoints(paths)
	if err != nil {
		t.Fatalf("fastapi detection failed: %v", err)
	}
	// Two different prefixes for one router: never guess a joined path, keep a
	// single fillable token instead.
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %+v", endpoints)
	}
	ep := endpoints[0]
	if !strings.Contains(ep.Path, "[include_prefix]") || !strings.HasSuffix(ep.Path, "/search") {
		t.Errorf("expected a fillable include prefix, got %q", ep.Path)
	}
	if len(ep.PathParams) != 1 || ep.PathParams[0].Name != "include_prefix" || ep.PathParams[0].Type != UnresolvedPathParamType {
		t.Errorf("expected one unresolved include_prefix param, got %+v", ep.PathParams)
	}
}
