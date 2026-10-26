package frameworks

import "testing"

func TestPlaceholderToken(t *testing.T) {
	cases := []struct {
		expr string
		line int
		want string
	}{
		{"API_PREFIX", 3, "[API_PREFIX]"},
		{" settings.API_PREFIX ", 3, "[settings.API_PREFIX]"},
		{"process.env.API_PREFIX", 7, "[process.env.API_PREFIX]"},
		{"getConfig()", 24, "[exprL24]"},
		{"", 9, "[exprL9]"},
	}
	for _, tc := range cases {
		if got := placeholderToken(tc.expr, tc.line); got != tc.want {
			t.Errorf("placeholderToken(%q, %d) = %q, want %q", tc.expr, tc.line, got, tc.want)
		}
	}
}

func TestUnresolvedParams(t *testing.T) {
	params := unresolvedParams("/[API_PREFIX]/books/[exprL24]")
	if len(params) != 2 {
		t.Fatalf("expected 2 unresolved params, got %+v", params)
	}
	if params[0].Name != "API_PREFIX" || params[0].Type != UnresolvedPathParamType {
		t.Errorf("unexpected first param %+v", params[0])
	}
	if params[1].Name != "exprL24" || params[1].Type != UnresolvedPathParamType {
		t.Errorf("unexpected second param %+v", params[1])
	}
	if got := unresolvedParams("/api/v1/books/:id"); len(got) != 0 {
		t.Errorf("expected no unresolved params for a resolved path, got %+v", got)
	}
}

func TestMergePathParams(t *testing.T) {
	merged := mergePathParams(
		[]PathParam{{Name: "id", Type: "string"}},
		[]PathParam{{Name: "API_PREFIX", Type: UnresolvedPathParamType}, {Name: "id", Type: UnresolvedPathParamType}},
	)
	if len(merged) != 2 {
		t.Fatalf("expected 2 merged params, got %+v", merged)
	}
	if merged[0].Name != "id" || merged[0].Type != "string" {
		t.Errorf("expected the route param to win, got %+v", merged[0])
	}
	if merged[1].Name != "API_PREFIX" {
		t.Errorf("expected the unresolved param to be kept, got %+v", merged[1])
	}
}
