package tracer

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"time"
)

// Hit uses runtime reflection to capture the caller's identity and emits it as JSON-RPC to stderr.
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
		Event:     "call",
		Name:      fn.Name(),
		File:      file,
		Line:      line,
		Timestamp: float64(time.Now().UnixNano()) / 1e9,
	}

	data, _ := json.Marshal(event)
	fmt.Fprintf(os.Stderr, "%s\n", string(data))

	return func() {}
}
