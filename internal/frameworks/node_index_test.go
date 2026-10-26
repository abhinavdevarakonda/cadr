package frameworks

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func writeNodeProject(t *testing.T, files map[string]string) []string {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func detectNodeProject(t *testing.T, files map[string]string) []Endpoint {
	t.Helper()
	endpoints, err := DetectNodeEndpoints(writeNodeProject(t, files))
	if err != nil {
		t.Fatalf("node detection failed: %v", err)
	}
	return endpoints
}

func requireRoute(t *testing.T, endpoints []Endpoint, key string) Endpoint {
	t.Helper()
	routes := endpointMap(endpoints)
	ep, ok := routes[key]
	if !ok {
		t.Fatalf("missing %s (got %+v)", key, endpoints)
	}
	return ep
}

// The reported bug: the prefix lives in settings.js and the route is a
// concatenation of the destructured require.
func TestNodeResolvesDestructuredRequireConstant(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"settings.js": "const API_PREFIX = '/api/v1';\nmodule.exports = { API_PREFIX };\n",
		"routes/books.js": `
const express = require('express');
const router = express.Router();
const { API_PREFIX } = require('../settings');

function appError(err, req, res, next) { next(err); }

router.get(API_PREFIX + '/books', booksController.list, appError);
`,
	})

	ep := requireRoute(t, endpoints, "GET /api/v1/books")
	if ep.HandlerFunc != "booksController.list" {
		t.Errorf("expected the controller to be the handler, got %q", ep.HandlerFunc)
	}
	if len(ep.PathParams) != 0 {
		t.Errorf("expected no unresolved params, got %+v", ep.PathParams)
	}
}

func TestNodeResolvesNamespaceRequireMember(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"settings.js": "module.exports = { API_PREFIX: '/api/v2' };\n",
		"routes.js": `
const express = require('express');
const settings = require('./settings');
const router = express.Router();

router.get(settings.API_PREFIX + '/books', booksController.list);
`,
	})
	requireRoute(t, endpoints, "GET /api/v2/books")
}

func TestNodeResolvesRenamedImport(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"settings.ts": "export const API_PREFIX = '/api/v3';\n",
		"routes.ts": `
import express, { Router } from 'express';
import { API_PREFIX as P } from './settings';

const router = Router();
router.get(P + '/books', booksController.list);
`,
	})
	requireRoute(t, endpoints, "GET /api/v3/books")
}

func TestNodeResolvesReexportHop(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"base.js":     "module.exports = { API_PREFIX: '/api/v4' };\n",
		"settings.js": "module.exports = require('./base');\n",
		"routes.js": `
const express = require('express');
const { API_PREFIX } = require('./settings');
const router = express.Router();

router.get(API_PREFIX + '/books', booksController.list);
`,
	})
	requireRoute(t, endpoints, "GET /api/v4/books")
}

func TestNodeResolvesTemplateLiteral(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"settings.js": "const API_PREFIX = '/api/v5';\nmodule.exports = { API_PREFIX };\n",
		"routes.js": `
const express = require('express');
const { API_PREFIX } = require('./settings');
const router = express.Router();

router.get(` + "`${API_PREFIX}/books`" + `, booksController.list);
`,
	})
	requireRoute(t, endpoints, "GET /api/v5/books")
}

func TestNodeResolvesOutOfOrderConcatenatedConsts(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"routes.js": `
const express = require('express');
const router = express.Router();

router.get(PREFIX + '/books', booksController.list);

const PREFIX = BASE + '/v1';
const BASE = '/api';
`,
	})
	requireRoute(t, endpoints, "GET /api/v1/books")
}

func TestNodeResolvesEnvDefault(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"routes.js": `
const express = require('express');
const router = express.Router();
const API_PREFIX = process.env.API_PREFIX || '/api/v7';

router.get(API_PREFIX + '/books', booksController.list);
`,
	})
	requireRoute(t, endpoints, "GET /api/v7/books")
}

func TestNodeResolvesAsConstAssertion(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"settings.ts": "export const API_PREFIX = '/api/v8' as const;\n",
		"routes.ts": `
import express, { Router } from 'express';
import { API_PREFIX } from './settings';

const router = Router();
router.get(API_PREFIX + '/books', booksController.list);
`,
	})
	requireRoute(t, endpoints, "GET /api/v8/books")
}

func TestNodeResolvesUniqueProjectName(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"helpers.js": "const PREFIX = '/api/v9';\n",
		"routes.js": `
const express = require('express');
const router = express.Router();

router.get(PREFIX + '/books', booksController.list);
`,
	})
	requireRoute(t, endpoints, "GET /api/v9/books")
}

func TestNodeAmbiguousPrefixBecomesFillable(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"one/settings.js": "const API_PREFIX = '/one';\n",
		"two/settings.js": "const API_PREFIX = '/two';\n",
		"routes.js": `
const express = require('express');
const router = express.Router();

router.get(API_PREFIX + '/books', booksController.list);
`,
	})

	ep := requireRoute(t, endpoints, "GET /[API_PREFIX]/books")
	if len(ep.PathParams) != 1 || ep.PathParams[0].Name != "API_PREFIX" || ep.PathParams[0].Type != UnresolvedPathParamType {
		t.Fatalf("expected an unresolved API_PREFIX param, got %+v", ep.PathParams)
	}
}

func TestNodeResolvesMountPrefixConstant(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"settings.js": "const API_PREFIX = '/api/v10';\nmodule.exports = { API_PREFIX };\n",
		"routes.js": `
const express = require('express');
const { API_PREFIX } = require('./settings');
const app = express();
const booksRouter = express.Router();

booksRouter.get('/books', booksController.list);
app.use(API_PREFIX, booksRouter);
`,
	})
	requireRoute(t, endpoints, "GET /api/v10/books")
}

func TestNodeResolvesFastifyRegisterPrefixConstant(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"settings.js": "const API_PREFIX = '/api/v11';\nmodule.exports = { API_PREFIX };\n",
		"routes.js": `
const fastify = require('fastify')();
const { API_PREFIX } = require('./settings');

fastify.register(booksPlugin, { prefix: API_PREFIX });

async function booksPlugin(instance) {
  instance.get('/books', booksController.list);
}
`,
	})
	requireRoute(t, endpoints, "GET /api/v11/books")
}

func TestNodeUnresolvedMountPrefixBecomesFillable(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"routes.js": `
const express = require('express');
const app = express();
const booksRouter = express.Router();

booksRouter.get('/books', booksController.list);
app.use(getPrefix(), booksRouter);
`,
	})

	routes := endpointMap(endpoints)
	found := false
	for key, ep := range routes {
		if key == "GET /[exprL7]/books" {
			found = true
			if len(ep.PathParams) != 1 || ep.PathParams[0].Type != UnresolvedPathParamType {
				t.Errorf("expected an unresolved mount param, got %+v", ep.PathParams)
			}
		}
	}
	if !found {
		t.Fatalf("expected a placeholder mount prefix, got %+v", endpoints)
	}
}

func TestNodeReexportCycleTerminates(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"a.js": "module.exports = require('./b');\n",
		"b.js": "module.exports = require('./a');\n",
		"routes.js": `
const express = require('express');
const { API_PREFIX } = require('./a');
const router = express.Router();

router.get(API_PREFIX + '/books', booksController.list);
`,
	})
	requireRoute(t, endpoints, "GET /[API_PREFIX]/books")
}

func TestNodeIgnoresNonRouterCalls(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"plain.js": `
const express = require('express');
const map = new Map();
map.get('/not-a-route', () => {});
map.post('/also-not', () => {});
`,
	})
	if len(endpoints) != 0 {
		t.Fatalf("expected no endpoints, got %+v", endpoints)
	}
}

func TestNodeUnresolvedFastifyMethodIsSkipped(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"routes.js": `
const fastify = require('fastify')();
const METHOD = process.env.METHOD;

fastify.route({ method: METHOD, url: '/dyn', handler: dynamicHandler });
`,
	})
	if len(endpoints) != 0 {
		t.Fatalf("expected no endpoint for an unresolvable method, got %+v", endpoints)
	}
}

func TestNodeMiddlewareOnlyUseIsNotAMountPrefix(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"routes.js": `
const express = require('express');
const app = express();
const router = express.Router();

router.get('/books', booksController.list);
app.use(logger, bodyParser);
app.use(router);
`,
	})
	requireRoute(t, endpoints, "GET /books")
}

func TestNodeMountPrefixBeforeRouterDeclaration(t *testing.T) {
	endpoints := detectNodeProject(t, map[string]string{
		"settings.js": "const API_PREFIX = '/api/v12';\nmodule.exports = { API_PREFIX };\n",
		"routes.js": `
const express = require('express');
const { API_PREFIX } = require('./settings');
const app = express();

app.use(API_PREFIX, booksRouter);

const booksRouter = express.Router();
booksRouter.get('/books', booksController.list);
`,
	})
	requireRoute(t, endpoints, "GET /api/v12/books")
}
