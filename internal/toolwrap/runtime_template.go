package toolwrap

import (
	"crypto/sha256"
	"encoding/hex"
	"hash/fnv"
	"os"
	"strconv"
	"strings"
)

// instrumentVersion must be bumped whenever the injected runtime or the
// instrumentation strategy changes. It is folded into the isolated GOCACHE
// directory name so that stale instrumented archives are never reused after a
// cadr upgrade.
const instrumentVersion = "3"

// emitterNamespaceBits splits the int64 span/seq space into per-package
// namespaces. The injected runtime seeds span/seq with `namespace << bits`, so
// counters from different packages never collide. This literal must match the
// shift in runtimeBody below.
const emitterNamespaceBits = 40

// Trace modes. Full injects an enter+deferred-exit pair (call tree +
// durations); light keeps the original single entry call (flat, lowest cost).
const (
	ModeFull  = "full"
	ModeLight = "light"
)

// TraceModeFromEnv reads CADR_TRACE_MODE, defaulting to full.
func TraceModeFromEnv() string {
	if v := os.Getenv("CADR_TRACE_MODE"); v == ModeLight {
		return ModeLight
	}
	return ModeFull
}

// runtimeBody is the source of the self-contained tracing runtime that cadr
// injects into every package it instruments. It uses only the standard library
// and declares every symbol with a __cadr_ prefix so it cannot collide with
// user code.
//
// It writes newline-delimited JSON matching tracer.Event to the CADR_SOCKET
// unix socket (or CADR_TCP) and falls back to stderr when no listener is
// reachable, mirroring internal/agents/py_trace.py.
//
// Full mode emits __cadr_enter (returns a span id, ev:"enter") and
// __cadr_exit(span) (ev:"exit"). The builder maps span->enter to compute
// durations, so the runtime keeps no span table and needs no lock.
const runtimeBody = `
import (
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

var (
	__cadr_once    sync.Once
	__cadr_ch      chan []byte
	__cadr_live    int32
	__cadr_local   int32
	__cadr_wmu     sync.Mutex
	__cadr_flushCh = make(chan struct{}, 1)
	__cadr_start   = time.Now()
	__cadr_span    = int64({CADR_NS}) << 40
	__cadr_seq     = int64({CADR_NS}) << 40
	__cadr_pid     = os.Getpid()
)

func __cadr_init() {
	if os.Getenv("CADR_LOCAL_ONLY") == "1" {
		atomic.StoreInt32(&__cadr_local, 1)
		return
	}
	__cadr_ch = make(chan []byte, 8192)
	go __cadr_sender()
}

// __cadr_enter emits ev:"enter" and returns the span id used to pair the exit.
func __cadr_enter(fn, file string, line int) int64 {
	__cadr_once.Do(__cadr_init)
	span := atomic.AddInt64(&__cadr_span, 1)
	seq := atomic.AddInt64(&__cadr_seq, 1)
	__cadr_send(__cadr_enter_line(fn, file, line, seq, span, __cadr_ts(), __cadr_gid()))
	return span
}

// __cadr_exit emits ev:"exit" for a span returned by __cadr_enter.
func __cadr_exit(span int64) {
	seq := atomic.AddInt64(&__cadr_seq, 1)
	__cadr_send(__cadr_exit_line(seq, span, __cadr_ts(), __cadr_gid()))
}

// __cadr_flush drains the async sender. Injected as the first defer of an
// instrumented main so the root exit is written before the process tears the
// sender goroutine down. A nil message is the barrier: the FIFO sender
// signals once it has written everything enqueued before it.
func __cadr_flush() {
	if atomic.LoadInt32(&__cadr_local) == 1 || __cadr_ch == nil {
		return
	}
	select {
	case __cadr_ch <- nil:
	case <-time.After(time.Second):
		return
	}
	select {
	case <-__cadr_flushCh:
	case <-time.After(2 * time.Second):
	}
}

// __cadr_trace is the legacy single-call entry used in light mode.
func __cadr_trace(fn, file string, line int) {
	__cadr_once.Do(__cadr_init)
	__cadr_send(__cadr_line(fn, file, line))
}

func __cadr_ts() int64 { return time.Since(__cadr_start).Nanoseconds() }

// __cadr_gid parses the goroutine id out of runtime.Stack's header
// ("goroutine 42 [running]:"). Portable across Go versions and platforms.
func __cadr_gid() int64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	i := 0
	for i < n && buf[i] != ' ' {
		i++
	}
	i++
	var id int64
	for i < n && buf[i] >= '0' && buf[i] <= '9' {
		id = id*10 + int64(buf[i]-'0')
		i++
	}
	return id
}

func __cadr_send(msg []byte) {
	if atomic.LoadInt32(&__cadr_local) == 1 {
		__cadr_write(msg)
		return
	}
	select {
	case __cadr_ch <- msg:
	default:
	}
	if atomic.LoadInt32(&__cadr_live) == 0 {
		__cadr_write(msg)
	}
}

func __cadr_write(msg []byte) {
	__cadr_wmu.Lock()
	_, _ = os.Stderr.Write(msg)
	__cadr_wmu.Unlock()
}

func __cadr_sender() {
	for {
		var conn net.Conn
		var err error
		if sock := os.Getenv("CADR_SOCKET"); sock != "" {
			conn, err = net.Dial("unix", sock)
		} else {
			port := os.Getenv("CADR_TCP")
			if port == "" {
				port = "9876"
			}
			conn, err = net.Dial("tcp", "127.0.0.1:"+port)
		}
		if err != nil {
			atomic.StoreInt32(&__cadr_live, 0)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		atomic.StoreInt32(&__cadr_live, 1)
		for msg := range __cadr_ch {
			if msg == nil {
				select {
				case __cadr_flushCh <- struct{}{}:
				default:
				}
				continue
			}
			if _, err := conn.Write(msg); err != nil {
				atomic.StoreInt32(&__cadr_live, 0)
				_ = conn.Close()
				break
			}
		}
	}
}

func __cadr_enter_line(fn, file string, line int, seq, span, ts, gid int64) []byte {
	b := make([]byte, 0, len(fn)+len(file)+128)
	b = append(b, "{\"lang\":\"go\",\"fn\":\""...)
	b = __cadr_esc(b, fn)
	b = append(b, "\",\"file\":\""...)
	b = __cadr_esc(b, file)
	b = append(b, "\",\"line\":"...)
	b = __cadr_itoa(b, line)
	b = append(b, ",\"ev\":\"enter\",\"ctx\":\""...)
	b = __cadr_itoa64(b, gid)
	b = append(b, "\",\"span\":"...)
	b = __cadr_itoa64(b, span)
	b = append(b, ",\"seq\":"...)
	b = __cadr_itoa64(b, seq)
	b = append(b, ",\"pid\":"...)
	b = __cadr_itoa(b, __cadr_pid)
	b = append(b, ",\"ts\":"...)
	b = __cadr_itoa64(b, ts)
	b = append(b, ",\"args\":{}}\n"...)
	return b
}

func __cadr_exit_line(seq, span, ts, gid int64) []byte {
	b := make([]byte, 0, 96)
	b = append(b, "{\"ev\":\"exit\",\"ctx\":\""...)
	b = __cadr_itoa64(b, gid)
	b = append(b, "\",\"span\":"...)
	b = __cadr_itoa64(b, span)
	b = append(b, ",\"seq\":"...)
	b = __cadr_itoa64(b, seq)
	b = append(b, ",\"pid\":"...)
	b = __cadr_itoa(b, __cadr_pid)
	b = append(b, ",\"ts\":"...)
	b = __cadr_itoa64(b, ts)
	b = append(b, "}\n"...)
	return b
}

// __cadr_line is the legacy (light mode) event: flat, enter-only.
func __cadr_line(fn, file string, line int) []byte {
	b := make([]byte, 0, len(fn)+len(file)+48)
	b = append(b, "{\"fn\":\""...)
	b = __cadr_esc(b, fn)
	b = append(b, "\",\"file\":\""...)
	b = __cadr_esc(b, file)
	b = append(b, "\",\"line\":"...)
	b = __cadr_itoa(b, line)
	b = append(b, ",\"args\":{}}\n"...)
	return b
}

func __cadr_esc(b []byte, s string) []byte {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			if c < 0x20 {
				const hex = "0123456789abcdef"
				b = append(b, '\\', 'u', '0', '0', hex[c>>4], hex[c&0x0f])
			} else {
				b = append(b, c)
			}
		}
	}
	return b
}

func __cadr_itoa(b []byte, n int) []byte { return __cadr_itoa64(b, int64(n)) }

func __cadr_itoa64(b []byte, n int64) []byte {
	if n == 0 {
		return append(b, '0')
	}
	if n < 0 {
		b = append(b, '-')
		n = -n
	}
	var tmp [20]byte
	i := len(tmp)
	for n > 0 {
		i--
		tmp[i] = byte('0' + n%10)
		n /= 10
	}
	return append(b, tmp[i:]...)
}
`

// PackageNamespace derives a 16-bit namespace from a package directory. It is
// stable for a build and keeps the per-package injected runtimes' span/seq
// counters collision-free.
func PackageNamespace(dir string) uint16 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(dir))
	return uint16(h.Sum32())
}

// runtimeSource returns the full source of the injected runtime file for the
// given package name and namespace. The namespace is substituted into the
// span/seq seed so per-package counters never collide across packages.
func runtimeSource(pkg string, ns uint16) string {
	body := strings.ReplaceAll(runtimeBody, "{CADR_NS}", strconv.Itoa(int(ns)))
	return "// Code generated by cadr. DO NOT EDIT.\n\npackage " + pkg + "\n" + body
}

// CacheKey returns a short, stable fingerprint of the injected runtime and the
// injection mode. It isolates the Go build cache per instrumentation version so
// switching between full and light never reuses stale instrumented archives.
func CacheKey(mode string) string {
	h := sha256.Sum256([]byte(instrumentVersion + "|" + mode + "|" + runtimeBody))
	return hex.EncodeToString(h[:])[:12]
}
