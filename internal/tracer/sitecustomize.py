import os

# only trace if cadr asked us to
if os.environ.get("CADR_TRACE") == "1":
    try:
        import py_trace
        py_trace.start()
    except Exception as e:
        import sys
        print(f"cadr trace injection failed: {e}", file=sys.stderr)
