package frameworks

import (
	"fmt"
	"regexp"
	"strings"
)

// UnresolvedPathParamType marks a PathParam that was not written in the source
// and could not be resolved statically. The TUI renders it as a fillable field
// whose value is substituted for the matching [NAME] token in the path.
const UnresolvedPathParamType = "unresolved"

var (
	placeholderNameRegex = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)
	unresolvedTokenRegex = regexp.MustCompile(`\[([A-Za-z0-9_.]+)\]`)
)

// placeholderToken renders a path expression that could not be resolved as a
// fillable token: `[API_PREFIX]` for identifier-like expressions and
// `[exprL<line>]` for anything else (calls, conditionals, literals in other
// shapes) so two unresolved expressions on different lines stay distinct.
func placeholderToken(expr string, line int) string {
	trimmed := strings.TrimSpace(expr)
	if placeholderNameRegex.MatchString(trimmed) {
		return "[" + trimmed + "]"
	}
	return fmt.Sprintf("[exprL%d]", line)
}

// unresolvedParams extracts the placeholder tokens left inside a path so the
// endpoint carries a PathParam the user can fill in.
func unresolvedParams(path string) []PathParam {
	matches := unresolvedTokenRegex.FindAllStringSubmatch(path, -1)
	if len(matches) == 0 {
		return nil
	}
	params := make([]PathParam, 0, len(matches))
	for _, m := range matches {
		params = append(params, PathParam{Name: m[1], Type: UnresolvedPathParamType})
	}
	return params
}

// mergePathParams concatenates parameter lists and keeps the first occurrence
// of each name (route params such as `:id` come before placeholders).
func mergePathParams(groups ...[]PathParam) []PathParam {
	var merged []PathParam
	seen := make(map[string]bool)
	for _, group := range groups {
		for _, p := range group {
			if p.Name == "" || seen[p.Name] {
				continue
			}
			seen[p.Name] = true
			merged = append(merged, p)
		}
	}
	return merged
}
