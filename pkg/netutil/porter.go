// Package netutil pkg/netutil/porter.go
package netutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	// PorterMinEphemeral is the default minimum ephemeral port.
	PorterMinEphemeral = uint16(49152)
)

// PorterValue associates a port value alongside it's children.
type PorterValue struct {
	Value    interface{}
	Children map[uint16]interface{}
}

// Porter reserves ports.
type Porter struct {
	sync.RWMutex
	eph        uint16 // current ephemeral value
	minEph     uint16 // minimal ephemeral port value
	ports      map[uint16]PorterValue
	reservedAt map[uint16]time.Time // tracks when each ephemeral port was reserved
	reservedBy map[uint16]string    // tracks caller of ReserveEphemeral (file:line)
	reserved   uint64               // total reservations (monotonic)
	freed      uint64               // total frees (monotonic)
}

// NewPorter creates a new Porter with a given minimum ephemeral port value.
func NewPorter(minEph uint16) *Porter {
	ports := make(map[uint16]PorterValue)
	ports[0] = PorterValue{} // port 0 is invalid

	return &Porter{
		eph:        minEph,
		minEph:     minEph,
		ports:      ports,
		reservedAt: make(map[uint16]time.Time),
		reservedBy: make(map[uint16]string),
	}
}

// Reserve a given port.
// It returns a boolean informing whether the port is reserved, and a function to clear the reservation.
func (p *Porter) Reserve(port uint16, v interface{}) (bool, func()) {
	p.Lock()
	defer p.Unlock()

	if _, ok := p.ports[port]; ok {
		return false, nil
	}
	p.ports[port] = PorterValue{
		Value: v,
	}
	return true, p.makePortFreer(port)
}

// ReserveChild reserves a child.
func (p *Porter) ReserveChild(port, subPort uint16, v interface{}) (bool, func()) {
	p.Lock()
	defer p.Unlock()

	pv, ok := p.ports[port]
	if !ok {
		return false, nil
	}
	if pv.Children == nil {
		pv.Children = make(map[uint16]interface{}, 1)
	} else if _, ok := pv.Children[subPort]; ok {
		return false, nil
	}

	pv.Children[subPort] = v
	p.ports[port] = pv
	return true, p.makeChildFreer(port, subPort)
}

// ErrEphemeralPortSpace is returned when no ephemeral ports are available.
var ErrEphemeralPortSpace = errors.New("ephemeral port space exhausted")

// Count returns the number of currently reserved ports.
func (p *Porter) Count() int {
	p.RLock()
	defer p.RUnlock()
	// Exclude the reserved port 0 entry.
	return len(p.ports) - 1
}

// ResetEphemeral removes all ephemeral port reservations (ports >= minEph),
// freeing the port space. Well-known ports (listeners) are preserved.
// Use this to recover from ephemeral port exhaustion without restarting.
func (p *Porter) ResetEphemeral() int {
	p.Lock()
	defer p.Unlock()
	freed := 0
	for port := range p.ports {
		if port >= p.minEph {
			delete(p.ports, port)
			delete(p.reservedAt, port)
			delete(p.reservedBy, port)
			freed++
		}
	}
	p.eph = p.minEph
	return freed
}

// ReserveEphemeral reserves a new ephemeral port.
// It returns the reserved ephemeral port, a function to clear the reservation and an error (if any).
// Returns ErrEphemeralPortSpace if all ephemeral ports (minEph through 65535) are occupied.
func (p *Porter) ReserveEphemeral(ctx context.Context, v interface{}) (uint16, func(), error) {
	p.Lock()
	defer p.Unlock()

	// Scan at most the full ephemeral range (minEph..65535).
	// The old code had no bound, spinning forever when all ports were taken
	// while holding the mutex — burning 100% CPU on map lookups.
	maxRange := uint32(65535-p.minEph) + 1
	for i := uint32(0); i < maxRange; i++ {
		select {
		case <-ctx.Done():
			return 0, nil, ctx.Err()
		default:
		}

		p.eph++
		if p.eph < p.minEph {
			p.eph = p.minEph
		}
		if _, ok := p.ports[p.eph]; ok {
			continue
		}
		p.ports[p.eph] = PorterValue{Value: v}
		p.reservedAt[p.eph] = time.Now()
		// Capture caller chain for leak diagnostics.
		// Build a short stack: skip frames to get meaningful callers.
		var callers []string
		for skip := 2; skip <= 5; skip++ {
			if _, file, line, ok := runtime.Caller(skip); ok {
				short := file
				if idx := len(file) - 40; idx > 0 {
					short = "..." + file[idx:]
				}
				callers = append(callers, fmt.Sprintf("%s:%d", short, line))
			}
		}
		if len(callers) > 0 {
			p.reservedBy[p.eph] = callers[len(callers)-1] // deepest meaningful caller
		}
		p.reserved++
		return p.eph, p.makePortFreer(p.eph), nil
	}

	return 0, nil, ErrEphemeralPortSpace
}

// PortValue returns the value stored under a given port.
func (p *Porter) PortValue(port uint16) (interface{}, bool) {
	p.RLock()
	defer p.RUnlock()

	v, ok := p.ports[port]
	return v.Value, ok
}

// RangePortValues ranges all ports that are currently reserved.
func (p *Porter) RangePortValues(fn func(port uint16, v interface{}) (next bool)) {
	p.RLock()
	defer p.RUnlock()

	for port, v := range p.ports {
		if next := fn(port, v.Value); !next {
			return
		}
	}
}

// RangePortValuesAndChildren ranges port values and it's contained children.
func (p *Porter) RangePortValuesAndChildren(fn func(port uint16, v PorterValue) (next bool)) {
	p.RLock()
	defer p.RUnlock()

	for port, v := range p.ports {
		if next := fn(port, v); !next {
			return
		}
	}
}

// This returns a function that frees a given port.
// It is ensured that the function's action is only performed once.
// The port entry is always deleted — any remaining children are orphaned
// because the parent stream they belonged to is gone.
func (p *Porter) makePortFreer(port uint16) func() {
	once := new(sync.Once)

	action := func() {
		p.Lock()
		defer p.Unlock()

		delete(p.ports, port)
		delete(p.reservedAt, port)
		delete(p.reservedBy, port)
		p.freed++
	}

	return func() { once.Do(action) }
}

func (p *Porter) makeChildFreer(port, subPort uint16) func() {
	once := new(sync.Once)

	action := func() {
		p.Lock()
		defer p.Unlock()

		if v, ok := p.ports[port]; ok && v.Children != nil {
			delete(v.Children, subPort)

			// Also delete the ensure port entry if port value is nil and there is no more children.
			if v.Value == nil && len(v.Children) == 0 {
				delete(p.ports, port)
			}
		}
	}

	return func() { once.Do(action) }
}

// EphemeralDiag returns diagnostic information about ephemeral port reservations.
// For each ephemeral port, it reports whether the stored value is nil, its type,
// and (for Stringer values) a short string representation. This helps identify
// what is holding ephemeral ports when the porter approaches exhaustion.
type EphemeralDiagEntry struct {
	Port      uint16 `json:"port"`
	ValueType string `json:"value_type"` // e.g. "*dmsg.Stream", "<nil>"
	ValueInfo string `json:"value_info,omitempty"`
	Children  int    `json:"children,omitempty"`
}

type EphemeralDiagResult struct {
	Total         int                  `json:"total"`
	Ephemeral     int                  `json:"ephemeral"`
	Listeners     int                  `json:"listeners"`
	NilValues     int                  `json:"nil_values"`
	TotalReserved uint64               `json:"total_reserved"` // monotonic counter
	TotalFreed    uint64               `json:"total_freed"`    // monotonic counter
	LeakRate      string               `json:"leak_rate"`      // current - freed as percentage
	TypeCount     map[string]int       `json:"type_count"`
	AgeBucket     map[string]int       `json:"age_bucket"`             // "0-30s", "30s-2m", "2m-5m", "5m-30m", "30m+"
	CallerCount   map[string]int       `json:"caller_count,omitempty"` // caller file:line → count (for entries >2min)
	Sample        []EphemeralDiagEntry `json:"sample,omitempty"`       // first 20 entries
}

func (p *Porter) EphemeralDiag() EphemeralDiagResult {
	p.RLock()
	defer p.RUnlock()

	now := time.Now()
	r := EphemeralDiagResult{
		Total:         len(p.ports),
		TotalReserved: p.reserved,
		TotalFreed:    p.freed,
		TypeCount:     make(map[string]int),
		AgeBucket: map[string]int{
			"0-30s":  0,
			"30s-2m": 0,
			"2m-5m":  0,
			"5m-30m": 0,
			"30m+":   0,
		},
	}
	for port, pv := range p.ports {
		if port < p.minEph {
			r.Listeners++
			continue
		}
		r.Ephemeral++
		typeName := "<nil>"
		if pv.Value != nil {
			typeName = fmt.Sprintf("%T", pv.Value)
		} else {
			r.NilValues++
		}
		r.TypeCount[typeName]++

		// Age bucket + caller tracking for old entries
		if t, ok := p.reservedAt[port]; ok {
			age := now.Sub(t)
			switch {
			case age < 30*time.Second:
				r.AgeBucket["0-30s"]++
			case age < 2*time.Minute:
				r.AgeBucket["30s-2m"]++
			case age < 5*time.Minute:
				r.AgeBucket["2m-5m"]++
				// Track callers for entries that should have been freed
				if caller, ok := p.reservedBy[port]; ok {
					if r.CallerCount == nil {
						r.CallerCount = make(map[string]int)
					}
					r.CallerCount[caller]++
				}
			case age < 30*time.Minute:
				r.AgeBucket["5m-30m"]++
				if caller, ok := p.reservedBy[port]; ok {
					if r.CallerCount == nil {
						r.CallerCount = make(map[string]int)
					}
					r.CallerCount[caller]++
				}
			default:
				r.AgeBucket["30m+"]++
			}
		}

		if len(r.Sample) < 20 {
			entry := EphemeralDiagEntry{
				Port:      port,
				ValueType: typeName,
				Children:  len(pv.Children),
			}
			if s, ok := pv.Value.(fmt.Stringer); ok {
				entry.ValueInfo = s.String()
			}
			r.Sample = append(r.Sample, entry)
		}
	}
	if r.TotalReserved > 0 {
		leaked := r.TotalReserved - r.TotalFreed
		pct := float64(leaked) / float64(r.TotalReserved) * 100
		r.LeakRate = fmt.Sprintf("%d/%d (%.1f%%)", leaked, r.TotalReserved, pct)
	}
	return r
}

// SweepStaleEphemeral closes and frees ephemeral port entries older than maxAge.
// It calls Close() on entries whose Value implements io.Closer, then deletes
// the porter entry. Returns the number of entries swept.
//
// This is a safety net for the DMSG stream lifecycle: if the normal cleanup
// paths (Stream.Read auto-release, explicit Close, session reaping) miss an
// entry, the sweeper catches it before the 16K port space is exhausted.
func (p *Porter) SweepStaleEphemeral(maxAge time.Duration) int {
	// Phase 1: identify stale ports and delete them from the map.
	// Do NOT call Close() while holding the lock — Stream.Close()
	// calls closeFn() which also acquires the porter lock → deadlock.
	p.Lock()
	now := time.Now()
	type sweepEntry struct {
		port  uint16
		value interface{}
	}
	var toSweep []sweepEntry
	for port, t := range p.reservedAt {
		if port >= p.minEph && now.Sub(t) > maxAge {
			pv := p.ports[port]
			toSweep = append(toSweep, sweepEntry{port: port, value: pv.Value})
			delete(p.ports, port)
			delete(p.reservedAt, port)
			delete(p.reservedBy, port)
			p.freed++
		}
	}
	p.Unlock()

	// Phase 2: close the values outside the lock with a timeout.
	// Stream.Close() can block indefinitely if the yamux TCP connection
	// is hung, so we give each Close 5 seconds before abandoning it.
	for _, e := range toSweep {
		if c, ok := e.value.(io.Closer); ok {
			done := make(chan struct{})
			go func() {
				c.Close() //nolint:errcheck,gosec
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
		}
	}
	return len(toSweep)
}

// CloseAll closes all contained variables that implement io.Closer
func (p *Porter) CloseAll(log logrus.FieldLogger) {
	if log == nil {
		l := logrus.New()
		l.SetOutput(os.Stderr)
		log = l
	}

	wg := new(sync.WaitGroup)
	p.Lock()
	for _, v := range p.ports {
		if c, ok := v.Value.(io.Closer); ok {

			wg.Add(1)
			go func(c io.Closer) {
				if err := c.Close(); err != nil {
					log.WithError(err).
						Debug("On (*netutil.Porter).CloseAll(), closing contained value resulted in error.")
				}
				wg.Done()
			}(c)
		}
	}
	p.Unlock()
	wg.Wait()
}
