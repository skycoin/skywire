// Package charts pkg/deployment/charts/service_stats.go c4-net-discovery
package charts

import (
	"context"
	"net/http"
	"os"
	"runtime"
	"runtime/metrics"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/httputil"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// Sample keys a ServiceStats adds to a service's chart samples.
const (
	keyCPU        = "proc.cpu"
	keyGC         = "proc.gc"
	keyRSS        = "proc.rss"
	keyHeap       = "proc.heap"
	keyGoroutines = "proc.goroutines"
	keyFDs        = "proc.fds"
	prefixIn      = "net.in."
	prefixOut     = "net.out."
	prefixStreams = "net.streams."
	prefixReq     = "http.req."
	prefixCode    = "http.code."
	prefixHTTPOut = "http.out."
	keyHTTPP50    = "http.p50"
	keyHTTPP95    = "http.p95"
)

// maxTimings bounds the response times kept between samples for percentiles.
const maxTimings = 4096

// ServiceStats samples what a service's process does and what its listeners
// carry, for the service's status page: CPU, memory, goroutines and open
// files of the process, bytes and streams per dmsg listening port (its HTTP
// API and each CXO feed), and HTTP requests by endpoint and status.
type ServiceStats struct {
	name string

	mu      sync.Mutex
	clients []*dmsg.Client
	names   map[uint16]string
	prevNet map[string]dmsg.PortTraffic

	prevCPU, prevGC float64
	prevAt          time.Time

	reqs    map[string]float64
	codes   map[string]float64
	out     map[string]float64
	timings []float64
}

// inProcess names every service that made a ServiceStats in this process,
// whose load the process figures include.
var inProcess struct {
	mu    sync.Mutex
	names []string
}

// NewServiceStats returns an empty ServiceStats for the named service.
func NewServiceStats(name string) *ServiceStats {
	inProcess.mu.Lock()
	inProcess.names = append(inProcess.names, name)
	inProcess.mu.Unlock()
	return &ServiceStats{name: name, names: map[uint16]string{}, prevNet: map[string]dmsg.PortTraffic{},
		reqs: map[string]float64{}, codes: map[string]float64{}, out: map[string]float64{}}
}

// sharedWith names the other services running in this process.
func (s *ServiceStats) sharedWith() string {
	inProcess.mu.Lock()
	defer inProcess.mu.Unlock()
	var others []string
	for _, n := range inProcess.names {
		if n != s.name {
			others = append(others, n)
		}
	}
	return strings.Join(others, " and ")
}

// CountDmsg counts the traffic c accepts on its listening ports.
func (s *ServiceStats) CountDmsg(c *dmsg.Client) {
	if s == nil || c == nil {
		return
	}
	c.CountTraffic()
	s.mu.Lock()
	s.clients = append(s.clients, c)
	s.mu.Unlock()
}

// NamePort names a dmsg listening port on the page. Unnamed ports show by number.
func (s *ServiceStats) NamePort(port uint16, name string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.names[port] = name
	s.mu.Unlock()
}

// Handler counts next's requests by route pattern, status class, bytes sent
// and time taken. An httputil.Router reports its pattern; anything else
// is counted by its first path segment.
func (s *ServiceStats) Handler(next http.Handler) http.Handler {
	if s == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r, routePattern := httputil.TrackRoutePattern(r)
		rw := &countingWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rw, r)
		pattern := *routePattern
		if pattern == "" {
			pattern = firstSegment(r.URL.Path)
		}
		s.record(r.Method+" "+pattern, rw.status, rw.n, time.Since(start))
	})
}

func (s *ServiceStats) record(endpoint string, status int, n int64, took time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqs[endpoint]++
	s.codes[strconv.Itoa(status/100)+"xx"]++
	s.out[endpoint] += float64(n)
	if len(s.timings) < maxTimings {
		s.timings = append(s.timings, float64(took)/float64(time.Millisecond))
	}
}

func firstSegment(p string) string {
	p = strings.TrimPrefix(p, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		p = p[:i]
	}
	return "/" + p
}

// countingWriter records the status and the bytes a handler writes.
type countingWriter struct {
	http.ResponseWriter
	status int
	n      int64
	wrote  bool
}

func (w *countingWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *countingWriter) Write(b []byte) (int, error) {
	w.wrote = true
	n, err := w.ResponseWriter.Write(b)
	w.n += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the writer underneath.
func (w *countingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush passes through to a writer that can flush.
func (w *countingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Collect adds this interval's figures to v: process use, the bytes and
// streams each listening port took since the last call, and the HTTP
// requests since then.
func (s *ServiceStats) Collect(v map[string]float64) {
	if s == nil {
		return
	}
	now := time.Now()
	cpu, gc := processCPU()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.prevAt.IsZero() {
		wall := now.Sub(s.prevAt).Seconds()
		if wall > 0 && cpu >= s.prevCPU {
			v[keyCPU] = (cpu - s.prevCPU) / wall * 100
			v[keyGC] = (gc - s.prevGC) / wall * 100
		}
	}
	s.prevCPU, s.prevGC, s.prevAt = cpu, gc, now
	if rss, ok := processRSS(); ok {
		v[keyRSS] = rss
	}
	v[keyHeap] = heapBytes()
	v[keyGoroutines] = float64(runtime.NumGoroutine())
	if n, ok := openFiles(); ok {
		v[keyFDs] = float64(n)
	}

	totals := map[string]dmsg.PortTraffic{}
	for _, c := range s.clients {
		for _, pt := range c.Traffic() {
			name := s.names[pt.Port]
			if name == "" {
				name = "port " + strconv.Itoa(int(pt.Port))
			}
			t := totals[name]
			t.Streams += pt.Streams
			t.In += pt.In
			t.Out += pt.Out
			totals[name] = t
		}
	}
	for name, t := range totals {
		p := s.prevNet[name]
		v[prefixIn+name] = float64(t.In - p.In)
		v[prefixOut+name] = float64(t.Out - p.Out)
		v[prefixStreams+name] = float64(t.Streams - p.Streams)
	}
	s.prevNet = totals

	for k, n := range s.reqs {
		v[prefixReq+k] = n
	}
	for k, n := range s.codes {
		v[prefixCode+k] = n
	}
	for k, n := range s.out {
		v[prefixHTTPOut+k] = n
	}
	if len(s.timings) > 0 {
		sort.Float64s(s.timings)
		v[keyHTTPP50] = s.timings[len(s.timings)/2]
		v[keyHTTPP95] = s.timings[len(s.timings)*95/100]
	}
	s.reqs, s.codes, s.out, s.timings = map[string]float64{}, map[string]float64{}, map[string]float64{}, s.timings[:0]
}

// Wrap adds this interval's figures to every sample collect takes.
func (s *ServiceStats) Wrap(collect Collect) Collect {
	if s == nil {
		return collect
	}
	return func(ctx context.Context) (map[string]float64, error) {
		v, err := collect(ctx)
		if err != nil {
			return v, err
		}
		if v == nil {
			v = map[string]float64{}
		}
		s.Collect(v)
		return v, nil
	}
}

// Charts draws the figures Collect sampled.
func (s *ServiceStats) Charts(f *Frame, from, to time.Time) []Chart {
	if s == nil {
		return nil
	}
	pct := func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) + "%" }
	ms := func(v float64) string { return strconv.FormatFloat(v, 'f', 0, 64) + " ms" }
	chart := func(title, note string, kind Kind, format func(float64) string, series []Series) Chart {
		return Chart{Title: title, Note: note, Kind: kind, From: from, To: to, Times: f.Times, Format: format, Series: series}
	}
	process := "this service's process"
	if shared := s.sharedWith(); shared != "" {
		process = "the process this service shares with the " + shared
	}
	out := []Chart{
		chart("Process CPU", "Share of one core used by "+process+", and by its garbage collector.", Lines, pct, []Series{
			{Name: "cpu", Vals: f.Values(keyCPU, false)},
			{Name: "garbage collection", Vals: f.Values(keyGC, false)},
		}),
		chart("Process memory", "Resident memory of "+process+", and the Go heap in use.", Lines, Bytes, []Series{
			{Name: "resident", Vals: f.Values(keyRSS, false)},
			{Name: "heap", Vals: f.Values(keyHeap, false)},
		}),
		chart("Goroutines and open files", "Goroutines running in "+process+", and the files and sockets it holds open.", Lines, nil, []Series{
			{Name: "goroutines", Vals: f.Values(keyGoroutines, false)},
			{Name: "open files", Vals: f.Values(keyFDs, false)},
		}),
	}
	if len(f.Keys(prefixOut)) > 0 {
		out = append(out,
			chart("Bytes sent, by listener", "What this service sent over dmsg per 5 minutes, by the API or CXO feed it went out on.", Stacked, Bytes, f.Group(prefixOut, 8)),
			chart("Bytes received, by listener", "What this service received over dmsg per 5 minutes, by listener.", Stacked, Bytes, f.Group(prefixIn, 8)),
			chart("Streams accepted, by listener", "Connections accepted per 5 minutes, by listener. A CXO subscriber opens one per sync.", Stacked, nil, f.Group(prefixStreams, 8)),
		)
	}
	if len(f.Keys(prefixReq)) > 0 {
		out = append(out,
			chart("HTTP requests, by endpoint", "Requests per 5 minutes, by method and route.", Stacked, nil, f.Group(prefixReq, 8)),
			chart("HTTP responses, by status", "Responses per 5 minutes, by status class.", Stacked, nil, f.Group(prefixCode, 0)),
			chart("HTTP bytes sent, by endpoint", "Response bytes per 5 minutes, by method and route.", Stacked, Bytes, f.Group(prefixHTTPOut, 8)),
			chart("HTTP response time", "Median and 95th percentile time to answer, per 5 minutes.", Lines, ms, []Series{
				{Name: "median", Vals: f.Values(keyHTTPP50, false)},
				{Name: "95th percentile", Vals: f.Values(keyHTTPP95, false)},
			}),
		)
	}
	return out
}

// processCPU returns the process's user+system CPU seconds and the runtime's
// estimate of its garbage collector's.
func processCPU() (cpu, gc float64) {
	s := []metrics.Sample{{Name: "/cpu/classes/gc/total:cpu-seconds"}}
	metrics.Read(s)
	if s[0].Value.Kind() == metrics.KindFloat64 {
		gc = s[0].Value.Float64()
	}
	if b, err := os.ReadFile("/proc/self/stat"); err == nil {
		// Fields after the parenthesized command name; utime and stime are
		// the 14th and 15th fields, in clock ticks (USER_HZ, 100 on Linux).
		if i := strings.LastIndexByte(string(b), ')'); i >= 0 {
			f := strings.Fields(string(b)[i+1:])
			if len(f) > 12 {
				u, _ := strconv.ParseFloat(f[11], 64)  //nolint:errcheck
				st, _ := strconv.ParseFloat(f[12], 64) //nolint:errcheck
				return (u + st) / 100, gc
			}
		}
	}
	u := []metrics.Sample{{Name: "/cpu/classes/user:cpu-seconds"}}
	metrics.Read(u)
	if u[0].Value.Kind() == metrics.KindFloat64 {
		cpu = u[0].Value.Float64() + gc
	}
	return cpu, gc
}

// processRSS is the resident set size, where /proc has it.
func processRSS() (float64, bool) {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseFloat(f[1], 64)
	if err != nil {
		return 0, false
	}
	return pages * float64(os.Getpagesize()), true
}

func heapBytes() float64 {
	s := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(s)
	if s[0].Value.Kind() == metrics.KindUint64 {
		return float64(s[0].Value.Uint64())
	}
	return 0
}

// openFiles counts the process's open descriptors, where /proc has them.
func openFiles() (int, bool) {
	d, err := os.Open("/proc/self/fd")
	if err != nil {
		return 0, false
	}
	defer d.Close() //nolint:errcheck
	names, err := d.Readdirnames(-1)
	if err != nil {
		return 0, false
	}
	return len(names), true
}
