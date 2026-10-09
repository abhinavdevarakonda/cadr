package tracer

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
)

// SchemaVersion is the trace event schema emitted by the current cadr. It
// lives in the run manifest (RunMeta.Schema), never per event, so a stream can
// be identified by its manifest while legacy v1 files remain readable.
const SchemaVersion = 2

// Event types. A legacy v1 line (no "ev" field) is normalized to EvEnter.
const (
	EvEnter = "enter"
	EvExit  = "exit"
)

// NormalizeEvent fills in the defaults that make a legacy v1 event behave like
// a schema v2 enter event. It is idempotent.
func NormalizeEvent(e Event) Event {
	if e.Ev == "" {
		e.Ev = EvEnter
	}
	return e
}

// ParseEvent decodes a single JSONL line into a normalized Event.
func ParseEvent(line []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(line, &e); err != nil {
		return e, err
	}
	return NormalizeEvent(e), nil
}

// DecodeEvents reads newline-delimited JSON events. Blank lines and lines that
// are not valid JSON are skipped so a stray log line on the transport does not
// abort a whole run.
func DecodeEvents(r io.Reader) ([]Event, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var events []Event
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		e, err := ParseEvent(line)
		if err != nil {
			continue
		}
		events = append(events, e)
	}
	if err := sc.Err(); err != nil {
		return events, err
	}
	return events, nil
}

// ReadEvents loads and normalizes a JSONL trace file.
func ReadEvents(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return DecodeEvents(f)
}

// EnterEvents returns only the enter events, preserving order. Exits annotate
// their matching enter and are intentionally dropped from row-oriented views
// (TUI navigation, status counts).
func EnterEvents(events []Event) []Event {
	out := make([]Event, 0, len(events))
	for _, e := range events {
		if NormalizeEvent(e).Ev == EvEnter {
			out = append(out, NormalizeEvent(e))
		}
	}
	return out
}
