package pfilter

import (
	"net"
	"sync"

	"golang.org/x/net/ipv4"
)

var (
	errTimeout = &netError{
		msg:       "i/o timeout",
		timeout:   true,
		temporary: true,
	}
	errClosed = &netError{
		msg:       "use of closed network connection",
		timeout:   false,
		temporary: false,
	}
	errNotSupported = &netError{
		msg:       "not supported",
		timeout:   false,
		temporary: false,
	}

	// Compile time interface assertion.
	_ net.Error = (*netError)(nil)
)

type netError struct {
	msg       string
	timeout   bool
	temporary bool
}

func (e *netError) Error() string   { return e.msg }
func (e *netError) Timeout() bool   { return e.timeout }
func (e *netError) Temporary() bool { return e.temporary }

type filteredConnList []*filteredConn

func (r filteredConnList) Len() int           { return len(r) }
func (r filteredConnList) Swap(i, j int)      { r[i], r[j] = r[j], r[i] }
func (r filteredConnList) Less(i, j int) bool { return r[i].priority < r[j].priority }

type messageWithError struct {
	ipv4.Message
	Err error
}

func (m *messageWithError) Copy(pool *sync.Pool) messageWithError {
	buf := pool.Get().([]byte)
	oobBuf := pool.Get().([]byte)

	copy(buf, m.Buffers[0][:m.N])
	if m.NN > 0 {
		copy(oobBuf, m.OOB[:m.NN])
	}

	return messageWithError{
		Message: ipv4.Message{
			Buffers: [][]byte{buf[:m.N]},
			OOB:     oobBuf[:m.NN],
			Addr:    m.Addr,
			N:       m.N,
			NN:      m.NN,
			Flags:   m.Flags,
		},
		Err: m.Err,
	}
}
