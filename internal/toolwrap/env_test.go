package toolwrap

import "testing"

func TestQuoteArg(t *testing.T) {
	if got := quoteArg("/usr/local/bin/cadr"); got != "/usr/local/bin/cadr" {
		t.Fatalf("plain = %q", got)
	}
	if got := quoteArg("/Program Files/cadr"); got != `"/Program Files/cadr"` {
		t.Fatalf("spaced = %q", got)
	}
	if got := quoteArg(`C:\Program Files\cadr.exe`); got != `"C:\Program Files\cadr.exe"` {
		t.Fatalf("windows = %q", got)
	}
}

func TestToolexecValue(t *testing.T) {
	if got := ToolexecValue("/usr/local/bin/cadr"); got != "/usr/local/bin/cadr tool-wrap" {
		t.Fatalf("value = %q", got)
	}
	if got := ToolexecValue("/Program Files/cadr"); got != `"/Program Files/cadr" tool-wrap` {
		t.Fatalf("spaced value = %q", got)
	}
}
