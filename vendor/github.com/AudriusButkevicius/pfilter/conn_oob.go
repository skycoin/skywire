package pfilter

import (
	"io"
	"net"
	"time"
)

type oobPacketConn interface {
	ReadMsgUDP(b, oob []byte) (n, oobn, flags int, addr *net.UDPAddr, err error)
	WriteMsgUDP(b, oob []byte, addr *net.UDPAddr) (n, oobn int, err error)
}

var _ oobPacketConn = (*filteredConnObb)(nil)

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
	case pkt := <-r.recvBuffer:
		err := pkt.err

		n := pkt.n
		if l := len(b); l < n {
			n = l
			if err == nil {
				err = io.ErrShortBuffer
			}
		}
		copy(b, pkt.buf[:n])

		oobn := pkt.oobn
		if oobl := len(oob); oobl < oobn {
			oobn = oobl
		}
		if oobn > 0 {
			copy(oob, pkt.oobBuf[:oobn])
		}

		r.source.bufPool.Put(pkt.buf[:r.source.packetSize])
		r.source.bufPool.Put(pkt.oobBuf[:r.source.packetSize])

		return n, oobn, pkt.flags, pkt.udpAddr, err
	case <-r.closed:
		return 0, 0, 0, nil, errClosed
	}
}
