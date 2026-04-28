package tracer

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
)

// hit uses runtime reflection to capture the caller's identity and emits it as JSON to stderr.
func Hit() func() {
	pc, file, line, ok := runtime.Caller(1)
	if !ok {
		return func() {}
	}

	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return func() {}
	}

	event := Event{
		Name: fn.Name(),
		File: file,
		Line: line,
		Args: map[string]interface{}{},
	}

	data, _ := json.Marshal(event)
	fmt.Fprintf(os.Stderr, "%s\n", string(data))

	return func() {}
}
