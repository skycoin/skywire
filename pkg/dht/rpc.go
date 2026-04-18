package dht

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// RPC method tags.
const (
	methodPing     uint8 = 1
	methodFindNode uint8 = 2
	methodGetValue uint8 = 3
	methodPutValue uint8 = 4
)

// rpcTimeout is the deadline for a single RPC round-trip.
const rpcTimeout = 10 * time.Second

// PingRequest is sent to check if a peer is alive.
type PingRequest struct {
	SenderID NodeID        `json:"sender_id"`
	SenderPK cipher.PubKey `json:"sender_pk"`
}

// PingResponse is the reply to a Ping.
type PingResponse struct {
	ResponderID NodeID        `json:"responder_id"`
	ResponderPK cipher.PubKey `json:"responder_pk"`
}

// FindNodeRequest asks a peer for the K closest nodes to a target.
type FindNodeRequest struct {
	SenderID NodeID        `json:"sender_id"`
	SenderPK cipher.PubKey `json:"sender_pk"`
	Target   NodeID        `json:"target"`
}

// FindNodeResponse returns the closest known peers.
type FindNodeResponse struct {
	Closest []Peer `json:"closest"`
}

// GetValueRequest asks a peer for a stored value at the given target.
type GetValueRequest struct {
	SenderID NodeID        `json:"sender_id"`
	SenderPK cipher.PubKey `json:"sender_pk"`
	Target   NodeID        `json:"target"`
}

// GetValueResponse returns either the item (if found) or the closest peers.
type GetValueResponse struct {
	Item    *MutableItem `json:"item,omitempty"`
	Closest []Peer       `json:"closest,omitempty"`
}

// PutValueRequest asks a peer to store a mutable item.
type PutValueRequest struct {
	SenderID NodeID        `json:"sender_id"`
	SenderPK cipher.PubKey `json:"sender_pk"`
	Item     MutableItem   `json:"item"`
}

// PutValueResponse indicates whether the item was stored.
type PutValueResponse struct {
	Stored bool   `json:"stored"`
	Error  string `json:"error,omitempty"`
}

// Transport is the interface for sending/receiving DHT RPC messages.
// Implementations include DMSG streams (production) and in-process
// pipes (testing).
type Transport interface {
	// Dial opens a bidirectional stream to a remote peer.
	Dial(ctx context.Context, pk cipher.PubKey) (io.ReadWriteCloser, error)
	// Listen returns a listener that accepts incoming streams.
	Listen() (Listener, error)
}

// Listener accepts incoming RPC streams.
type Listener interface {
	Accept() (io.ReadWriteCloser, cipher.PubKey, error)
	Close() error
}

// writeMsg writes a length-prefixed, tagged JSON message to w.
func writeMsg(w io.Writer, method uint8, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("dht rpc: marshal: %w", err)
	}
	// [4-byte length][1-byte method][payload]
	header := make([]byte, 5)
	binary.BigEndian.PutUint32(header[:4], uint32(1+len(data)))
	header[4] = method
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// readMsg reads a length-prefixed, tagged JSON message from r.
func readMsg(r io.Reader) (method uint8, data []byte, err error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(header[:4])
	if length < 1 || length > 1<<20 { // sanity: max 1MB
		return 0, nil, fmt.Errorf("dht rpc: invalid message length %d", length)
	}
	method = header[4]
	data = make([]byte, length-1)
	if _, err := io.ReadFull(r, data); err != nil {
		return 0, nil, err
	}
	return method, data, nil
}

// rpcCall performs a single RPC call: write request, read response.
func rpcCall(ctx context.Context, rw io.ReadWriteCloser, method uint8, req, resp interface{}) error {
	done := make(chan error, 1)
	go func() {
		if err := writeMsg(rw, method, req); err != nil {
			done <- fmt.Errorf("dht rpc write: %w", err)
			return
		}
		_, data, err := readMsg(rw)
		if err != nil {
			done <- fmt.Errorf("dht rpc read: %w", err)
			return
		}
		done <- json.Unmarshal(data, resp)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		rw.Close() //nolint:errcheck,gosec
		return ctx.Err()
	}
}
