import sys
import json
import time

def trace_calls(frame, event, arg):
    # Callback for sys.settrace. Captures function calls and emits JSON metadata to stderr.
    if event != 'call':
        return None
    
    func_name = frame.f_code.co_name
    filename = frame.f_code.co_filename
    
    if any(x in filename for x in ["lib/", "site-packages", "<frozen"]):
        return None
    
    event_data = {
        "event": "call",
        "name": func_name,
        "file": filename,
        "line": frame.f_lineno,
        "timestamp": time.time()
    }
    print(json.dumps(event_data), file=sys.stderr, flush=True)
    return trace_calls

def run_script(path, args=[]):
    """Compiles and executes a Python script with call tracing enabled."""
    sys.argv = [path] + args
    with open(path, 'rb') as f:
        code = compile(f.read(), path, 'exec')
    
    sys.settrace(trace_calls)
    try:
        exec(code, {"__name__": "__main__"})
    finally:
        sys.settrace(None)

if __name__ == "__main__":
    if len(sys.argv) < 2:
        sys.exit(1)
    run_script(sys.argv[1], sys.argv[2:])
