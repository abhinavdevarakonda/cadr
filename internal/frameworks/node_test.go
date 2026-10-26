package frameworks

import (
	"os"
	"path/filepath"
	"testing"
)

func writeNodeFixture(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write %s: %v", name, err)
	}
	return path
}

func endpointMap(endpoints []Endpoint) map[string]Endpoint {
	m := make(map[string]Endpoint)
	for _, ep := range endpoints {
		m[ep.Method+" "+ep.Path] = ep
	}
	return m
}

func TestNodeExpressRouteDetection(t *testing.T) {
	bt := "`"
	jsCode := "const express = require('express');\n" +
		"const app = express();\n" +
		"const router = express.Router();\n" +
		"const API = '/v1';\n" +
		"\n" +
		"router.get('/users/:id', handlers.getUser);\n" +
		"router.post(API + '/items', handlers.createItem);\n" +
		"router.get(['/multi-a', '/multi-b'], handlers.multi);\n" +
		"\n" +
		"app.get('/health', (req, res) => res.json({ ok: true }));\n" +
		"app.post('/users', auth, handlers.createUser);\n" +
		"app.all('/any', handlers.any);\n" +
		"app.put(" + bt + "/tpl" + bt + ", handlers.tpl);\n" +
		"app.use('/api', router);\n" +
		"\n" +
		"config.get('/not-a-route', fallback);\n" +
		"storage.get('/also-not', fallback);\n"

	path := writeNodeFixture(t, "server.js", jsCode)

	endpoints, err := DetectNodeEndpoints([]string{path})
	if err != nil {
		t.Fatalf("node detection failed: %v", err)
	}

	routes := endpointMap(endpoints)
	expected := []string{
		"GET /api/users/:id",
		"POST /api/v1/items",
		"GET /api/multi-a",
		"GET /api/multi-b",
		"GET /health",
		"POST /users",
		"GET /any",
		"POST /any",
		"PUT /any",
		"PATCH /any",
		"DELETE /any",
		"PUT /tpl",
	}

	if len(endpoints) != len(expected) {
		t.Fatalf("expected %d endpoints, got %d: %+v", len(expected), len(endpoints), endpoints)
	}
	for _, key := range expected {
		if _, ok := routes[key]; !ok {
			t.Errorf("missing %s (got %+v)", key, endpoints)
		}
	}
	for _, unexpected := range []string{"GET /not-a-route", "GET /also-not"} {
		if _, ok := routes[unexpected]; ok {
			t.Errorf("unexpected endpoint %s from a non-router receiver", unexpected)
		}
	}

	if ep, ok := routes["POST /users"]; ok && ep.HandlerFunc != "handlers.createUser" {
		t.Errorf("expected handler handlers.createUser (last arg), got %q", ep.HandlerFunc)
	}
	if ep, ok := routes["GET /health"]; ok {
		if ep.HandlerFunc != "fn@10" {
			t.Errorf("expected inline handler fn@10, got %q", ep.HandlerFunc)
		}
		if ep.Framework != "node" {
			t.Errorf("expected framework node, got %q", ep.Framework)
		}
	}
	if ep, ok := routes["GET /api/users/:id"]; ok {
		if len(ep.PathParams) != 1 || ep.PathParams[0].Name != "id" {
			t.Errorf("expected path param id, got %+v", ep.PathParams)
		}
	}
}

func TestNodeFastifyRouteDetection(t *testing.T) {
	jsCode := `
const fastify = require('fastify')();

fastify.get('/ping', ping);
fastify.all('/all', all);
fastify.route({ method: 'GET', url: '/obj', handler: objHandler });
fastify.route({ method: ['GET', 'POST'], url: '/multi', handler: multiHandler });
fastify.register(routesPlugin, { prefix: '/v1' });

async function routesPlugin(fastify, opts) {
  fastify.get('/inner', innerHandler);
}
`
	path := writeNodeFixture(t, "fastify.js", jsCode)

	endpoints, err := DetectNodeEndpoints([]string{path})
	if err != nil {
		t.Fatalf("node detection failed: %v", err)
	}
	if len(endpoints) != 10 {
		t.Fatalf("expected 10 endpoints, got %d: %+v", len(endpoints), endpoints)
	}

	routes := endpointMap(endpoints)
	for _, key := range []string{
		"GET /ping",
		"GET /all", "POST /all", "PUT /all", "PATCH /all", "DELETE /all",
		"GET /obj",
		"GET /multi",
		"POST /multi",
		"GET /v1/inner",
	} {
		if _, ok := routes[key]; !ok {
			t.Errorf("missing %s (got %+v)", key, endpoints)
		}
	}
	if ep, ok := routes["GET /obj"]; ok && ep.HandlerFunc != "objHandler" {
		t.Errorf("expected handler objHandler, got %q", ep.HandlerFunc)
	}
}

func TestNodeKoaRouterDetection(t *testing.T) {
	jsCode := `
const Koa = require('koa');
const Router = require('koa-router');

const app = new Koa();
const router = new Router();

router.prefix('/koa');
router.get('/ping', async (ctx) => { ctx.body = 'ok'; });
router.post('/echo', ctx => {});
`
	path := writeNodeFixture(t, "koa.js", jsCode)

	endpoints, err := DetectNodeEndpoints([]string{path})
	if err != nil {
		t.Fatalf("node detection failed: %v", err)
	}

	routes := endpointMap(endpoints)
	for _, key := range []string{"GET /koa/ping", "POST /koa/echo"} {
		if _, ok := routes[key]; !ok {
			t.Errorf("missing %s (got %+v)", key, endpoints)
		}
	}
	if len(endpoints) != 2 {
		t.Errorf("expected 2 endpoints, got %d: %+v", len(endpoints), endpoints)
	}
}

func TestNodeTypeScriptRouteDetection(t *testing.T) {
	tsCode := `import express, { Router } from 'express';

interface User { id: number }

const app: express.Application = express();
const router: Router = Router();

app.get('/typed', (req, res) => res.send('ok'));
router.delete('/typed/:id', async (req, res) => {});
`
	path := writeNodeFixture(t, "server.ts", tsCode)

	endpoints, err := DetectNodeEndpoints([]string{path})
	if err != nil {
		t.Fatalf("node detection failed: %v", err)
	}

	routes := endpointMap(endpoints)
	for _, key := range []string{"GET /typed", "DELETE /typed/:id"} {
		if _, ok := routes[key]; !ok {
			t.Errorf("missing %s (got %+v)", key, endpoints)
		}
	}
	if len(endpoints) != 2 {
		t.Errorf("expected 2 endpoints, got %d: %+v", len(endpoints), endpoints)
	}
}

func TestNodeDetectionSkipsProjectsWithoutFramework(t *testing.T) {
	jsCode := `
const map = new Map();
map.get('/not-a-route', () => {});
map.post('/also-not-a-route', () => {});
`
	path := writeNodeFixture(t, "plain.js", jsCode)

	endpoints, err := DetectNodeEndpoints([]string{path})
	if err != nil {
		t.Fatalf("node detection failed: %v", err)
	}
	if len(endpoints) != 0 {
		t.Fatalf("expected no endpoints for a non-framework project, got %+v", endpoints)
	}
}
