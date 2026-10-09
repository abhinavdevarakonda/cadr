package toolwrap

// sharedRuntimeBody is the source of the single, process-wide tracing runtime
// package. Unlike the legacy per-package runtime (runtime_template.go), this
// package is compiled exactly once per build and imported by every instrumented
// file, so it owns one socket sender, one span counter and one seq counter for
// the whole process. That is what makes cross-package event order and span
// uniqueness exact.
//
// All state is initialised lazily via sync.Once, and the package is referenced
// through a real import, so its package initialisers run normally too.
const sharedRuntimeBody = `
import (
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

var (
	once    sync.Once
	ch      chan []byte
	live    int32
	local   int32
	wmu     sync.Mutex
	start   = time.Now()
	span    int64
	seq     int64
	pid     = os.Getpid()
	flushCh = make(chan struct{}, 1)
)

func initRuntime() {
	if os.Getenv("CADR_LOCAL_ONLY") == "1" {
		atomic.StoreInt32(&local, 1)
		return
	}
	ch = make(chan []byte, 8192)
	go sender()
}

// Enter emits ev:"enter" and returns the span id used to pair the exit.
func Enter(fn, file string, line int) int64 {
	once.Do(initRuntime)
	s := atomic.AddInt64(&span, 1)
	q := atomic.AddInt64(&seq, 1)
	send(enterLine(fn, file, line, q, s, ts(), gid()))
	return s
}

// Exit emits ev:"exit" for a span returned by Enter.
func Exit(s int64) {
	once.Do(initRuntime)
	q := atomic.AddInt64(&seq, 1)
	send(exitLine(q, s, ts(), gid()))
}

// Trace is the legacy single-call entry used in light mode (flat, no exit).
func Trace(fn, file string, line int) {
	once.Do(initRuntime)
	send(legacyLine(fn, file, line))
}

// Flush drains the async sender. Injected into main so the root exit is written
// before the process tears the sender goroutine down. It waits even when the
// sender has not connected yet, so a short-lived program still gets its events
// delivered once the listener accepts.
func Flush() {
	if atomic.LoadInt32(&local) == 1 || ch == nil {
		return
	}
	select {
	case ch <- nil:
	case <-time.After(time.Second):
		return
	}
	select {
	case <-flushCh:
	case <-time.After(2 * time.Second):
	}
}

func ts() int64 { return time.Since(start).Nanoseconds() }

// gid parses the goroutine id out of runtime.Stack's header
// ("goroutine 42 [running]:"). Portable across Go versions and platforms.
func gid() int64 {
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

func send(msg []byte) {
	if atomic.LoadInt32(&local) == 1 {
		write(msg)
		return
	}
	select {
	case ch <- msg:
	default:
	}
	if atomic.LoadInt32(&live) == 0 {
		write(msg)
	}
}

func write(msg []byte) {
	wmu.Lock()
	_, _ = os.Stderr.Write(msg)
	wmu.Unlock()
}

func sender() {
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
			atomic.StoreInt32(&live, 0)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		atomic.StoreInt32(&live, 1)
		for msg := range ch {
			if msg == nil {
				select {
				case flushCh <- struct{}{}:
				default:
				}
				continue
			}
			if _, err := conn.Write(msg); err != nil {
				atomic.StoreInt32(&live, 0)
				_ = conn.Close()
				break
			}
		}
	}
}

func enterLine(fn, file string, line int, seq, span, ts, gid int64) []byte {
	b := make([]byte, 0, len(fn)+len(file)+128)
	b = append(b, "{\"lang\":\"go\",\"fn\":\""...)
	b = esc(b, fn)
	b = append(b, "\",\"file\":\""...)
	b = esc(b, file)
	b = append(b, "\",\"line\":"...)
	b = itoa(b, line)
	b = append(b, ",\"ev\":\"enter\",\"ctx\":\""...)
	b = itoa64(b, gid)
	b = append(b, "\",\"span\":"...)
	b = itoa64(b, span)
	b = append(b, ",\"seq\":"...)
	b = itoa64(b, seq)
	b = append(b, ",\"pid\":"...)
	b = itoa(b, pid)
	b = append(b, ",\"ts\":"...)
	b = itoa64(b, ts)
	b = append(b, ",\"args\":{}}\n"...)
	return b
}

func exitLine(seq, span, ts, gid int64) []byte {
	b := make([]byte, 0, 96)
	b = append(b, "{\"ev\":\"exit\",\"ctx\":\""...)
	b = itoa64(b, gid)
	b = append(b, "\",\"span\":"...)
	b = itoa64(b, span)
	b = append(b, ",\"seq\":"...)
	b = itoa64(b, seq)
	b = append(b, ",\"pid\":"...)
	b = itoa(b, pid)
	b = append(b, ",\"ts\":"...)
	b = itoa64(b, ts)
	b = append(b, "}\n"...)
	return b
}

func legacyLine(fn, file string, line int) []byte {
	b := make([]byte, 0, len(fn)+len(file)+48)
	b = append(b, "{\"fn\":\""...)
	b = esc(b, fn)
	b = append(b, "\",\"file\":\""...)
	b = esc(b, file)
	b = append(b, "\",\"line\":"...)
	b = itoa(b, line)
	b = append(b, ",\"args\":{}}\n"...)
	return b
}

func esc(b []byte, s string) []byte {
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

func itoa(b []byte, n int) []byte { return itoa64(b, int64(n)) }

func itoa64(b []byte, n int64) []byte {
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

// sharedRuntimeSource returns the full source of the shared runtime package.
func sharedRuntimeSource() string {
	return "// Code generated by cadr. DO NOT EDIT.\n\npackage cadrruntime\n" + sharedRuntimeBody
}
