import sys
import json
import time
import socket
import threading
import queue
import os

_sock = None
_event_queue = queue.Queue()
_sock_connected = threading.Event()
_project_root = os.getcwd()

def _sender_thread():
    global _sock
    while True:
        try:
            _sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            _sock.connect(("localhost", 9876))
            _sock_connected.set()
            print("Maplet: Connected to monitor.", file=sys.stderr)
            
            while True:
                event = _event_queue.get()
                try:
                    line = (json.dumps(event) + "\n").encode('utf-8')
                    _sock.sendall(line)
                except:
                    _sock_connected.clear()
                    print("Maplet: Connection lost. Retrying...", file=sys.stderr)
                    break 
        except Exception:
            time.sleep(1) # Retry every second

def trace_calls(frame, event, arg):
    """Callback for sys.settrace. Captures function calls and emits JSON metadata."""
    if event != 'call':
        return None
    
    code = frame.f_code
    func_name = code.co_name
    filename = code.co_filename
    
    # Filter out library calls
    if any(x in filename for x in ["lib/", "site-packages", "<frozen"]):
        return None
    
    # Only trace project files
    if _project_root not in filename:
        return None

    event_data = {
        "event": "call",
        "name": func_name,
        "file": filename,
        "line": frame.f_lineno,
        "timestamp": time.time()
    }
    
    # Send to the background thread
    _event_queue.put(event_data)
    
    # Log to local stderr if not connected (fallback)
    if not _sock_connected.is_set():
        print(json.dumps(event_data), file=sys.stderr, flush=True)
        
    return trace_calls

def run_script(path, args=[]):
    """Compiles and executes a Python script with call tracing enabled."""
    # start background sender unless we are explicitly doing a local synchronous trace
    if os.environ.get("MAPLET_LOCAL_ONLY") != "1":
        t = threading.Thread(target=_sender_thread, daemon=True)
        t.start()
    else:
        # prevent stderr fallbacks from complaining about connection lost in trace_calls
        _sock_connected.clear()
    
    sys.argv = [path] + args
    
    # Normalize path to absolute
    abs_path = os.path.abspath(path)
    
    with open(abs_path, 'rb') as f:
        code = compile(f.read(), abs_path, 'exec')
    
    # Start tracing
    sys.settrace(trace_calls)
    try:
        # Execute in a namespace that looks like __main__
        global_ns = {
            "__name__": "__main__",
            "__file__": abs_path,
        }
        exec(code, global_ns)
    finally:
        sys.settrace(None)
        if _sock:
            _sock.close()

if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: py_trace.py <script> [args...]", file=sys.stderr)
        sys.exit(1)
    run_script(sys.argv[1], sys.argv[2:])
