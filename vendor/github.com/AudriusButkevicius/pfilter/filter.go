package pfilter

import (
	"errors"
	"net"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/quic-go/quic-go"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// These are both the same, socket.Message, just have type aliases.
//
//goland:noinspection GoVarAndConstTypeMayBeOmitted
var _ ipv4.Message = ipv6.Message{}

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

	// If non-zero, uses ipv4.PacketConn.ReadBatch, using the size of the batch given.
	// Defaults to 1 on Darwin/FreeBSD and 8 on Linux.
	BatchSize int
}

// NewPacketFilter creates a packet filter object wrapping the given packet
// connection.
func NewPacketFilter(conn net.PacketConn) *PacketFilter {
	// This is derived from quic codebase.
	var batchSize = 0
	switch runtime.GOOS {
	case "linux":
		batchSize = 8
	case "freebsd", "darwin":
		batchSize = 1
	}
	p, _ := NewPacketFilterWithConfig(Config{
		Conn:       conn,
		BufferSize: 1500,
		Backlog:    256,
		BatchSize:  batchSize,
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
		batchSize:  config.BatchSize,
		bufPool: sync.Pool{
			New: func() interface{} {
				return make([]byte, config.BufferSize)
			},
		},
	}
	if config.BatchSize > 0 {
		if _, ok := config.Conn.(*net.UDPConn); ok {
			d.ipv4Conn = ipv4.NewPacketConn(config.Conn)
		}
	}
	if oobConn, ok := d.conn.(quic.OOBCapablePacketConn); ok {
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
	oobConn    quic.OOBCapablePacketConn
	ipv4Conn   *ipv4.PacketConn
	packetSize int
	backlog    int
	batchSize  int
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
		recvBuffer: make(chan messageWithError, d.backlog),
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
	msgReader := d.readFrom
	if d.ipv4Conn != nil {
		msgReader = d.readBatch
	} else if d.oobConn != nil {
		msgReader = d.readMsgUdp
	}
	go d.loop(msgReader)
}

func (d *PacketFilter) readFrom() []messageWithError {
	buf := d.bufPool.Get().([]byte)
	n, addr, err := d.conn.ReadFrom(buf)

	return []messageWithError{
		{
			Message: ipv4.Message{
				Buffers: [][]byte{buf[:n]},
				Addr:    addr,
				N:       n,
			},
			Err: err,
		},
	}
}

func (d *PacketFilter) readBatch() []messageWithError {
	batch := make([]ipv4.Message, d.batchSize)
	for i := range batch {
		buf := d.bufPool.Get().([]byte)
		oobBuf := d.bufPool.Get().([]byte)
		batch[i].Buffers = [][]byte{buf}
		batch[i].OOB = oobBuf
	}

	n, err := d.ipv4Conn.ReadBatch(batch, 0)

	// This is entirely unexpected, but happens in the wild
	if n < 0 && err == nil {
		err = errUnexpectedNegativeLength
	}

	if err != nil {
		// Pretend we've read one message, so we reuse the first message of the batch for error
		// propagation.
		n = 1
	}

	result := make([]messageWithError, n)

	for i := 0; i < n; i++ {
		result[i].Err = err
		result[i].Message = batch[i]
	}

	for _, msg := range batch[n:] {
		d.returnBuffers(msg)
	}

	return result
}

var errUnexpectedNegativeLength = errors.New("ReadMsgUDP returned a negative number of read bytes")

func (d *PacketFilter) readMsgUdp() []messageWithError {
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

	return []messageWithError{
		{
			Message: ipv4.Message{
				Buffers: [][]byte{buf[:n]},
				OOB:     oobBuf[:oobn],
				Addr:    addr,
				N:       n,
				NN:      oobn,
				Flags:   flags,
			},
			Err: err,
		},
	}
}

func (d *PacketFilter) loop(msgReader func() []messageWithError) {
	for {
		msgs := msgReader()
		for _, msg := range msgs {
			if msg.Err != nil {
				if nerr, ok := msg.Err.(net.Error); ok && nerr.Temporary() {
					continue
				}
				d.mut.Lock()
				for _, conn := range d.conns {
					select {
					case conn.recvBuffer <- msg.Copy(&d.bufPool):
					default:
						atomic.AddUint64(&d.overflow, 1)
					}
				}
				d.mut.Unlock()
				d.returnBuffers(msg.Message)
				return
			}

			d.mut.Lock()
			sent := d.sendMessageLocked(msg)
			d.mut.Unlock()
			if !sent {
				atomic.AddUint64(&d.dropped, 1)
				d.returnBuffers(msg.Message)
			}
		}
	}
}

func (d *PacketFilter) returnBuffers(msg ipv4.Message) {
	for _, buf := range msg.Buffers {
		d.bufPool.Put(buf[:d.packetSize])
	}
	if msg.OOB != nil {
		d.bufPool.Put(msg.OOB[:d.packetSize])
	}
}

func (d *PacketFilter) sendMessageLocked(msg messageWithError) bool {
	for _, conn := range d.conns {
		if conn.filter == nil || conn.filter.ClaimIncoming(msg.Buffers[0], msg.Addr) {
			select {
			case conn.recvBuffer <- msg:
			default:
				atomic.AddUint64(&d.overflow, 1)
			}
			return true
		}
	}
	return false
}
