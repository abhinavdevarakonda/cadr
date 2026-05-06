import os

# only trace if cadr asked us to
if os.environ.get("MAPLET_TRACE") == "1":
    try:
        import py_trace
        py_trace.start()
    except Exception as e:
        import sys
        print(f"Maplet trace injection failed: {e}", file=sys.stderr)
