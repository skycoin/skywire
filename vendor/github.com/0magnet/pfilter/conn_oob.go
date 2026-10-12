package pfilter

import (
	"net"
	"time"

	"github.com/quic-go/quic-go"
)

var _ quic.OOBCapablePacketConn = (*filteredConnObb)(nil)

type filteredConnObb struct {
	*filteredConn
}

func (r *filteredConnObb) WriteMsgUDP(b, oob []byte, addr *net.UDPAddr) (n, oobn int, err error) {
	return r.source.oobConn.WriteMsgUDP(b, oob, addr)
}

func (r *filteredConnObb) ReadMsgUDP(b, oob []byte) (n, oobn, flags int, addr *net.UDPAddr, err error) {
	select {
	case <-r.closed:
		return 0, 0, 0, nil, errClosed
	default:
	}

	var timeout <-chan time.Time

	if deadline, ok := r.deadline.Load().(time.Time); ok && !deadline.IsZero() {
		timer := time.NewTimer(deadline.Sub(time.Now()))
		timeout = timer.C
		defer timer.Stop()
	}

	select {
	case <-timeout:
		return 0, 0, 0, nil, errTimeout
	case msg := <-r.recvBuffer:
		n, nn, err := copyBuffers(msg, b, oob)

		r.source.returnBuffers(msg.Message)

		udpAddr, ok := msg.Addr.(*net.UDPAddr)
		if !ok && err == nil {
			err = errNotSupported
		}

		return n, nn, msg.Flags, udpAddr, err
	case <-r.closed:
		return 0, 0, 0, nil, errClosed
	}
}
