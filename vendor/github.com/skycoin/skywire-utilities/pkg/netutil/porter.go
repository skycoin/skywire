// Package netutil pkg/netutil/porter.go
package netutil

import (
	"context"
	"io"
	"sync"

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
	eph    uint16 // current ephemeral value
	minEph uint16 // minimal ephemeral port value
	ports  map[uint16]PorterValue
}

// NewPorter creates a new Porter with a given minimum ephemeral port value.
func NewPorter(minEph uint16) *Porter {
	ports := make(map[uint16]PorterValue)
	ports[0] = PorterValue{} // port 0 is invalid

	return &Porter{
		eph:    minEph,
		minEph: minEph,
		ports:  ports,
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

// ReserveEphemeral reserves a new ephemeral port.
// It returns the reserved ephemeral port, a function to clear the reservation and an error (if any).
func (p *Porter) ReserveEphemeral(ctx context.Context, v interface{}) (uint16, func(), error) {
	p.Lock()
	defer p.Unlock()

	for {
		p.eph++
		if p.eph < p.minEph {
			p.eph = p.minEph
		}
		if _, ok := p.ports[p.eph]; ok {
			select {
			case <-ctx.Done():
				return 0, nil, ctx.Err()
			default:
				continue
			}
		}
		p.ports[p.eph] = PorterValue{Value: v}
		return p.eph, p.makePortFreer(p.eph), nil
	}
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

// This returns a function that frees a given port (if there are no children).
// It is ensured that the function's action is only performed once.
func (p *Porter) makePortFreer(port uint16) func() {
	once := new(sync.Once)

	action := func() {
		p.Lock()
		defer p.Unlock()

		// If port still has children, only clear the port value.
		if v, ok := p.ports[port]; ok && len(v.Children) > 0 {
			v.Value = nil
			p.ports[port] = v
			return
		}

		delete(p.ports, port)
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

// CloseAll closes all contained variables that implement io.Closer
func (p *Porter) CloseAll(log logrus.FieldLogger) {
	if log == nil {
		log = logrus.New()
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
