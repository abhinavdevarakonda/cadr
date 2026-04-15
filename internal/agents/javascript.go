package agents

import (
	"embed"
)

//go:embed js_trace.js
var javascriptFiles embed.FS

func init() {
	Register("javascript", Agent{
		Name:          "javascript",
		Files:         javascriptFiles,
		EnvVar:        "NODE_OPTIONS",
		EnvValue:      "--require {hookDir}/js_trace.js",
		TraceEnvVar:   "MAPLET_TRACE",
		TraceEnvValue: "1",
	})
}
