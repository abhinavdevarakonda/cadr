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
            print("cadr: Connected to monitor.", file=sys.stderr)
            
            while True:
                event = _event_queue.get()
                try:
                    line = (json.dumps(event) + "\n").encode('utf-8')
                    _sock.sendall(line)
                except:
                    _sock_connected.clear()
                    print("cadr: Connection lost. Retrying...", file=sys.stderr)
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
        
    # Filter out useless internal Python noise
    if func_name in ["<module>", "<genexpr>", "<listcomp>", "<dictcomp>", "__annotate__", "root"]:
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

def start():
    """Initializes the background sender and globally attaches the cadr trace hook."""
    # Start background sender unless we are explicitly doing a local synchronous trace
    if os.environ.get("CADR_LOCAL_ONLY") != "1":
        t = threading.Thread(target=_sender_thread, daemon=True)
        t.start()
    else:
        # Prevent stderr fallbacks from complaining about connection lost in trace_calls
        _sock_connected.clear()
    
    # Attach to the Main Thread
    sys.settrace(trace_calls)
    # Attach to all future Threads spawned by this Python process
    threading.settrace(trace_calls)
