package pfilter

import (
	"net"
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

type packet struct {
	n       int
	oobn    int
	flags   int
	addr    net.Addr
	udpAddr *net.UDPAddr
	err     error
	buf     []byte
	oobBuf  []byte
}
