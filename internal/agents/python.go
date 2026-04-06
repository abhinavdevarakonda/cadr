package agents

import (
	"embed"
)

//go:embed py_trace.py sitecustomize.py
var pythonFiles embed.FS

func init() {
	Register("python", Agent{
		Name:          "python",
		Files:         pythonFiles,
		EnvVar:        "PYTHONPATH",
		TraceEnvVar:   "MAPLET_TRACE",
		TraceEnvValue: "1",
	})
}
