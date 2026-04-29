// Package dht pkg/dht/transport_layer.go
//
// TransportLayerDHT implements the DHT Transport interface over skywire
// transports using DHTPacket (type 12) on route ID 0. This allows DHT
// synchronization without DMSG — messages hop between transport peers
// on the control channel, same as cascade route setup packets.
package dht

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	tp "github.com/skycoin/skywire/pkg/transport"
)

// TransportLayerDHT implements DHT Transport by sending/receiving
// DHTPacket frames on route ID 0 of skywire transports.
type TransportLayerDHT struct {
	log *logging.Logger
	tm  *tp.Manager

	// streams tracks virtual DHT connections keyed by (streamID).
	// Each stream is a bidirectional pipe between the DHT RPC layer
	// and the transport's route ID 0 packet handler.
	streams   map[uint64]*dhtStream
	streamsMu sync.Mutex
	streamID  uint64

	// incoming accepts virtual connections from remote peers.
	incoming chan *dhtStream
	done     chan struct{}
	once     sync.Once
}

// NewTransportLayerDHT creates a transport-layer DHT transport.
func NewTransportLayerDHT(tm *tp.Manager, log *logging.Logger) *TransportLayerDHT {
	return &TransportLayerDHT{
		log:      log,
		tm:       tm,
		streams:  make(map[uint64]*dhtStream),
		incoming: make(chan *dhtStream, 32),
		done:     make(chan struct{}),
	}
}

// dhtStreamHeader is prepended to DHTPacket payloads for multiplexing.
// [streamID:8][remotePK:33][flags:1][data...]
// flags: 0x01 = SYN (new stream), 0x02 = FIN (close), 0x00 = data
const (
	dhtHeaderSize = 8 + 33 + 1
	dhtFlagData   = 0x00
	dhtFlagSyn    = 0x01
	dhtFlagFin    = 0x02
)

// dhtStream is a virtual bidirectional connection over route ID 0 DHTPackets.
type dhtStream struct {
	id       uint64
	remotePK cipher.PubKey
	tpID     uuid.UUID // which transport this stream flows through
	readBuf  chan []byte
	closed   chan struct{}
	once     sync.Once
	parent   *TransportLayerDHT
}

func (s *dhtStream) Read(p []byte) (int, error) {
	select {
	case data, ok := <-s.readBuf:
		if !ok {
			return 0, io.EOF
		}
		n := copy(p, data)
		return n, nil
	case <-s.closed:
		return 0, io.EOF
	}
}

func (s *dhtStream) Write(p []byte) (int, error) {
	select {
	case <-s.closed:
		return 0, io.ErrClosedPipe
	default:
	}

	transport, err := s.parent.tm.GetTransportByID(s.tpID)
	if err != nil {
		return 0, fmt.Errorf("dht transport write: %w", err)
	}

	localPK := s.parent.localPK()
	payload := make([]byte, dhtHeaderSize+len(p))
	binary.BigEndian.PutUint64(payload[:8], s.id)
	copy(payload[8:41], localPK[:])
	payload[41] = dhtFlagData
	copy(payload[dhtHeaderSize:], p)

	pkt := makeDHTPacket(payload)
	return len(p), transport.WriteRawPacket(pkt)
}

func (s *dhtStream) Close() error {
	s.once.Do(func() {
		close(s.closed)
		s.parent.streamsMu.Lock()
		delete(s.parent.streams, s.id)
		s.parent.streamsMu.Unlock()
	})
	return nil
}

func (s *dhtStream) RemotePK() cipher.PubKey {
	return s.remotePK
}

func (tl *TransportLayerDHT) localPK() cipher.PubKey {
	// Get local PK from any network client in the transport manager.
	// This is safe because all clients share the same PK.
	return tl.tm.Local()
}

func (tl *TransportLayerDHT) nextStreamID() uint64 {
	tl.streamsMu.Lock()
	defer tl.streamsMu.Unlock()
	tl.streamID++
	return tl.streamID
}

// Dial opens a virtual DHT stream to a remote peer over an existing transport.
func (tl *TransportLayerDHT) Dial(ctx context.Context, pk cipher.PubKey) (io.ReadWriteCloser, error) {
	// Find a transport to this peer.
	transports := tl.tm.GetTransportsByLabels(tp.LabelAutomatic, tp.LabelSkycoin, tp.LabelSetup)
	var targetTp *tp.ManagedTransport
	for _, t := range transports {
		if t.Remote() == pk && !t.IsClosed() {
			targetTp = t
			break
		}
	}
	if targetTp == nil {
		return nil, fmt.Errorf("dht transport: no transport to %s", pk.String())
	}

	streamID := tl.nextStreamID()
	stream := &dhtStream{
		id:       streamID,
		remotePK: pk,
		tpID:     targetTp.Entry.ID,
		readBuf:  make(chan []byte, 64),
		closed:   make(chan struct{}),
		parent:   tl,
	}

	tl.streamsMu.Lock()
	tl.streams[streamID] = stream
	tl.streamsMu.Unlock()

	// Send SYN to establish the stream on the remote end.
	localPK := tl.localPK()
	payload := make([]byte, dhtHeaderSize)
	binary.BigEndian.PutUint64(payload[:8], streamID)
	copy(payload[8:41], localPK[:])
	payload[41] = dhtFlagSyn

	pkt := makeDHTPacket(payload)
	if err := targetTp.WriteRawPacket(pkt); err != nil {
		stream.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("dht transport: send SYN: %w", err)
	}

	return stream, nil
}

// Listen returns a listener that accepts incoming DHT streams.
func (tl *TransportLayerDHT) Listen() (Listener, error) {
	return &transportLayerListener{tl: tl}, nil
}

// HandleDHTPacket is the callback for DHTPacket frames arriving on route ID 0.
// Register this with the transport manager alongside the cascade handler.
func (tl *TransportLayerDHT) HandleDHTPacket(p routing.Packet, mt *tp.ManagedTransport) {
	if len(p.Payload()) < dhtHeaderSize {
		return
	}
	payload := p.Payload()
	streamID := binary.BigEndian.Uint64(payload[:8])
	var remotePK cipher.PubKey
	copy(remotePK[:], payload[8:41])
	flags := payload[41]
	data := payload[dhtHeaderSize:]

	switch flags {
	case dhtFlagSyn:
		// New incoming stream.
		stream := &dhtStream{
			id:       streamID,
			remotePK: remotePK,
			tpID:     mt.Entry.ID,
			readBuf:  make(chan []byte, 64),
			closed:   make(chan struct{}),
			parent:   tl,
		}
		tl.streamsMu.Lock()
		tl.streams[streamID] = stream
		tl.streamsMu.Unlock()

		select {
		case tl.incoming <- stream:
		default:
			tl.log.Warn("DHT transport: incoming stream dropped (buffer full)")
			stream.Close() //nolint:errcheck,gosec
		}

	case dhtFlagData:
		tl.streamsMu.Lock()
		stream, ok := tl.streams[streamID]
		tl.streamsMu.Unlock()
		if !ok {
			return // unknown stream, drop
		}
		// Copy data to avoid retaining the packet buffer.
		buf := make([]byte, len(data))
		copy(buf, data)
		select {
		case stream.readBuf <- buf:
		case <-stream.closed:
		default:
			tl.log.Warn("DHT transport: read buffer full, dropping data")
		}

	case dhtFlagFin:
		tl.streamsMu.Lock()
		stream, ok := tl.streams[streamID]
		if ok {
			delete(tl.streams, streamID)
		}
		tl.streamsMu.Unlock()
		if ok {
			stream.Close() //nolint:errcheck,gosec
		}
	}
}

// Close shuts down the transport layer DHT.
func (tl *TransportLayerDHT) Close() error {
	tl.once.Do(func() {
		close(tl.done)
		tl.streamsMu.Lock()
		for _, s := range tl.streams {
			s.Close() //nolint:errcheck,gosec
		}
		tl.streamsMu.Unlock()
	})
	return nil
}

type transportLayerListener struct {
	tl *TransportLayerDHT
}

func (l *transportLayerListener) Accept() (io.ReadWriteCloser, cipher.PubKey, error) {
	select {
	case stream := <-l.tl.incoming:
		return stream, stream.remotePK, nil
	case <-l.tl.done:
		return nil, cipher.PubKey{}, net.ErrClosed
	}
}

func (l *transportLayerListener) Close() error {
	return l.tl.Close()
}

func makeDHTPacket(payload []byte) routing.Packet {
	pkt := make([]byte, routing.PacketHeaderSize+len(payload))
	pkt[routing.PacketTypeOffset] = byte(routing.DHTPacket)
	binary.BigEndian.PutUint16(pkt[routing.PacketPayloadSizeOffset:], uint16(len(payload))) //nolint:gosec
	copy(pkt[routing.PacketPayloadOffset:], payload)
	return pkt
}
