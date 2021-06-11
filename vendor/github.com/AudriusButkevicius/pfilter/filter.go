package pfilter

import (
	"errors"
	"net"
	"sort"
	"sync"
	"sync/atomic"
)

// Filter object receives all data sent out on the Outgoing callback,
// and is expected to decide if it wants to receive the packet or not via
// the Receive callback
type Filter interface {
	Outgoing([]byte, net.Addr)
	ClaimIncoming([]byte, net.Addr) bool
}

type Config struct {
	Conn net.PacketConn

	// Size of the byte array passed to the read operations of the underlying
	// socket. Buffer that is too small could result in truncated reads. Defaults to 15000
	BufferSize int

	// Backlog of how many packets we are happy to buffer in memory
	Backlog int
}

// NewPacketFilter creates a packet filter object wrapping the given packet
// connection.
func NewPacketFilter(conn net.PacketConn) *PacketFilter {
	p, _ := NewPacketFilterWithConfig(Config{
		Conn:       conn,
		BufferSize: 1500,
		Backlog:    256,
	})
	return p
}

// NewPacketFilterWithConfig creates a packet filter object with the configuration provided
func NewPacketFilterWithConfig(config Config) (*PacketFilter, error) {
	if config.Conn == nil {
		return nil, errors.New("no connection provided")
	}
	if config.BufferSize < 1 {
		return nil, errors.New("invalid packet size")
	}
	if config.Backlog < 0 {
		return nil, errors.New("negative backlog")
	}

	d := &PacketFilter{
		conn:       config.Conn,
		packetSize: config.BufferSize,
		backlog:    config.Backlog,
		bufPool: sync.Pool{
			New: func() interface{} {
				return make([]byte, config.BufferSize)
			},
		},
	}
	if oobConn, ok := d.conn.(oobPacketConn); ok {
		d.oobConn = oobConn
	}
	return d, nil
}

// PacketFilter embeds a net.PacketConn to perform the filtering.
type PacketFilter struct {
	// Alignment
	dropped  uint64
	overflow uint64

	conn       net.PacketConn
	oobConn    oobPacketConn
	packetSize int
	backlog    int
	bufPool    sync.Pool

	conns []*filteredConn
	mut   sync.Mutex
}

// NewConn returns a new net.PacketConn object which filters packets based
// on the provided filter. If filter is nil, the connection will receive all
// packets. Priority decides which connection gets the ability to claim the packet.
func (d *PacketFilter) NewConn(priority int, filter Filter) net.PacketConn {
	conn := &filteredConn{
		priority:   priority,
		source:     d,
		recvBuffer: make(chan packet, d.backlog),
		filter:     filter,
		closed:     make(chan struct{}),
	}
	d.mut.Lock()
	d.conns = append(d.conns, conn)
	sort.Sort(filteredConnList(d.conns))
	d.mut.Unlock()
	if d.oobConn != nil {
		return &filteredConnObb{conn}
	}
	return conn
}

func (d *PacketFilter) removeConn(r *filteredConn) {
	d.mut.Lock()
	for i, conn := range d.conns {
		if conn == r {
			copy(d.conns[i:], d.conns[i+1:])
			d.conns[len(d.conns)-1] = nil
			d.conns = d.conns[:len(d.conns)-1]
			break
		}
	}
	d.mut.Unlock()
}

// NumberOfConns returns the number of currently active virtual connections
func (d *PacketFilter) NumberOfConns() int {
	d.mut.Lock()
	n := len(d.conns)
	d.mut.Unlock()
	return n
}

// Dropped returns number of packets dropped due to nobody claiming them.
func (d *PacketFilter) Dropped() uint64 {
	return atomic.LoadUint64(&d.dropped)
}

// Overflow returns number of packets were dropped due to receive buffers being
// full.
func (d *PacketFilter) Overflow() uint64 {
	return atomic.LoadUint64(&d.overflow)
}

// Start starts reading packets from the socket and forwarding them to connections.
// Should call this after creating all the expected connections using NewConn, otherwise the packets
// read will be dropped.
func (d *PacketFilter) Start() {
	pktReader := d.readFrom
	if d.oobConn != nil {
		pktReader = d.readMsgUdp
	}
	go d.loop(pktReader)
}

func (d *PacketFilter) readFrom() packet {
	buf := d.bufPool.Get().([]byte)
	n, addr, err := d.conn.ReadFrom(buf)

	return packet{
		n:    n,
		addr: addr,
		err:  err,
		buf:  buf[:n],
	}
}

var errUnexpectedNegativeLength = errors.New("ReadMsgUDP returned a negative number of read bytes")

func (d *PacketFilter) readMsgUdp() packet {
	buf := d.bufPool.Get().([]byte)
	oobBuf := d.bufPool.Get().([]byte)
	n, oobn, flags, addr, err := d.oobConn.ReadMsgUDP(buf, oobBuf)

	// This is entirely unexpected, but happens in the wild
	if n < 0 {
		if err == nil {
			err = errUnexpectedNegativeLength
		}
		n = 0
	}
	if oobn < 0 {
		if err == nil {
			err = errUnexpectedNegativeLength
		}
		oobn = 0
	}

	return packet{
		n:       n,
		oobn:    oobn,
		flags:   flags,
		addr:    addr,
		udpAddr: addr,
		err:     err,
		buf:     buf[:n],
		oobBuf:  oobBuf[:oobn],
	}
}

func (d *PacketFilter) loop(pktReader func() packet) {
	for {
		pkt := pktReader()
		if pkt.err != nil {
			if nerr, ok := pkt.err.(net.Error); ok && nerr.Temporary() {
				continue
			}
			d.mut.Lock()
			for _, conn := range d.conns {
				select {
				case conn.recvBuffer <- pkt:
				default:
					atomic.AddUint64(&d.overflow, 1)
				}
			}
			d.mut.Unlock()
			return
		}

		d.mut.Lock()
		sent := d.sendPacketLocked(pkt)
		d.mut.Unlock()
		if !sent {
			atomic.AddUint64(&d.dropped, 1)
			d.bufPool.Put(pkt.buf[:d.packetSize])
			if pkt.oobBuf != nil {
				d.bufPool.Put(pkt.oobBuf[:d.packetSize])
			}
		}
	}
}

func (d *PacketFilter) sendPacketLocked(pkt packet) bool {
	for _, conn := range d.conns {
		if conn.filter == nil || conn.filter.ClaimIncoming(pkt.buf, pkt.addr) {
			select {
			case conn.recvBuffer <- pkt:
			default:
				atomic.AddUint64(&d.overflow, 1)
			}
			return true
		}
	}
	return false
}
