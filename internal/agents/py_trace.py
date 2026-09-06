import sys
import json
import socket
import threading
import queue
import os
import time

_sock = None
_event_queue = queue.Queue()
_sock_connected = threading.Event()
_project_root = os.path.realpath(os.getcwd())

def _sender_thread():
    global _sock
    sock_path = os.environ.get("CADR_SOCKET")
    tcp_port_str = os.environ.get("CADR_TCP")
    use_uds = bool(sock_path and hasattr(socket, "AF_UNIX"))

    while True:
        try:
            if use_uds:
                _sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                _sock.connect(sock_path)
            else:
                port = int(tcp_port_str) if tcp_port_str else 9876
                _sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
                _sock.connect(("localhost", port))
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
    if not filename.startswith("<"):
        filename = os.path.realpath(os.path.abspath(filename))
    
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

def _extract_path_params(path):
    import re
    flask_param_regex = re.compile(r'<(?:([a-zA-Z_]\w*):)?([a-zA-Z_]\w*)>')
    params = []
    for match in flask_param_regex.finditer(path):
        ptype, pname = match.groups()
        if not ptype:
            ptype = "string"
        params.append({"name": pname, "type": ptype})
    return params

def _save_dynamic_endpoint(rule, view_func, methods):
    try:
        os.makedirs(".cadr/cache", exist_ok=True)
        cache_path = ".cadr/cache/endpoints.json"
        
        endpoints = []
        if os.path.exists(cache_path):
            try:
                with open(cache_path, "r", encoding="utf-8") as f:
                    endpoints = json.load(f)
            except Exception:
                pass

        filename = ""
        line = 0
        handler_name = ""
        if view_func:
            if hasattr(view_func, "__code__"):
                filename = os.path.abspath(view_func.__code__.co_filename)
                if filename.startswith(_project_root):
                    filename = os.path.relpath(filename, _project_root)
                line = view_func.__code__.co_firstlineno
            if hasattr(view_func, "__name__"):
                handler_name = view_func.__name__
            elif hasattr(view_func, "__class__"):
                handler_name = view_func.__class__.__name__

        if not methods:
            methods = ["GET"]

        for m in methods:
            m = m.upper()
            exists = False
            for ep in endpoints:
                if ep.get("path") == rule and ep.get("method") == m:
                    exists = True
                    break
            if not exists:
                endpoints.append({
                    "method": m,
                    "path": rule,
                    "handler_func": handler_name,
                    "file": filename,
                    "line": line,
                    "framework": "flask",
                    "path_params": _extract_path_params(rule)
                })

        with open(cache_path, "w", encoding="utf-8") as f:
            json.dump(endpoints, f, indent=2)
    except Exception as e:
        print(f"cadr: failed to save dynamic endpoint: {e}", file=sys.stderr)

def _patch_flask():
    try:
        import flask
        
        # Hook Flask.add_url_rule
        original_app_add = flask.Flask.add_url_rule
        def patched_app_add(self, rule, endpoint=None, view_func=None, **options):
            methods = options.get("methods")
            _save_dynamic_endpoint(rule, view_func, methods)
            return original_app_add(self, rule, endpoint, view_func, **options)
        flask.Flask.add_url_rule = patched_app_add
    except ImportError:
        pass
    except Exception as e:
        print(f"cadr: failed to patch flask: {e}", file=sys.stderr)

def start():
    """Initializes the background sender and globally attaches the cadr trace hook."""
    _patch_flask()
    
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
