import sys
import json
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

def _safe_repr(value):
    """Safely serialize a value, truncating large objects to avoid slowdowns."""
    try:
        if isinstance(value, (int, float, bool, type(None))):
            return value
        s = repr(value)
        return s if len(s) <= 120 else s[:120] + "..."
    except Exception:
        return "<unserializable>"

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
        
    # Filter out useless internal Python noise
    if func_name in ["<module>", "<genexpr>", "<listcomp>", "<dictcomp>", "__annotate__", "root"]:
        return None
    
    # Only trace project files
    if _project_root not in filename:
        return None

    # Capture parameter names (args defined in the function signature)
    arg_names = code.co_varnames[:code.co_argcount]
    args = {name: _safe_repr(frame.f_locals.get(name)) for name in arg_names}

    event_data = {
        "fn": func_name,
        "file": filename,
        "line": frame.f_lineno,
        "args": args,
    }
    
    # Send to the background thread
    _event_queue.put(event_data)
    
    # Log to local stderr if not connected (fallback)
    if not _sock_connected.is_set():
        print(json.dumps(event_data), file=sys.stderr, flush=True)
        
    return trace_calls

def start():
    """Initializes the background sender and globally attaches the Maplet trace hook."""
    # Start background sender unless we are explicitly doing a local synchronous trace
    if os.environ.get("MAPLET_LOCAL_ONLY") != "1":
        t = threading.Thread(target=_sender_thread, daemon=True)
        t.start()
    else:
        # Prevent stderr fallbacks from complaining about connection lost in trace_calls
        _sock_connected.clear()
    
    # Attach to the Main Thread
    sys.settrace(trace_calls)
    # Attach to all future Threads spawned by this Python process
    threading.settrace(trace_calls)
