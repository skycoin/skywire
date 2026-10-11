// Package transport pkg/transport/stream_digest.go c4-net-transport
package transport

import (
	"sync"

	"github.com/cespare/xxhash/v2"

	"github.com/skycoin/skywire/pkg/transport/network"
)

// CheckpointBase is the spacing in bytes of the finest checkpoint level.
// Level m is every CheckpointBase<<(4*m) bytes: 16 MiB, 256 MiB, 4 GiB, 64 GiB.
const CheckpointBase = 16 << 20

// CheckpointLevels is how many checkpoint levels a digest keeps.
const CheckpointLevels = 4

// Checkpoint is the digest of a connection's byte stream from its start up to
// Offset. Both ends of a reliable connection see the same stream, so equal
// checkpoints from the two edges prove those bytes passed between them.
type Checkpoint struct {
	Offset uint64
	Digest uint64
}

// DigestState is one direction of a transport's current connection.
type DigestState struct {
	// Epoch names the connection: its digest at the first checkpoint, the
	// same on both edges. Zero until the stream reaches CheckpointBase.
	Epoch uint64
	// Latest holds the most recent checkpoint of each level, zero where the
	// stream has not reached that level yet.
	Latest [CheckpointLevels]Checkpoint
}

// streamDigest hashes the bytes of one direction of one connection, in the
// order they cross it. Only the connection it was made for is counted.
type streamDigest struct {
	// wmu orders writes with their hashing, so the digest sees bytes in the
	// order they reach the conn. mu guards the state and is held only briefly.
	wmu   sync.Mutex
	mu    sync.Mutex
	conn  network.Transport
	h     *xxhash.Digest
	n     uint64
	state DigestState
}

// reset starts over for conn, a connection just installed.
func (d *streamDigest) reset(conn network.Transport) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.conn, d.n, d.state = conn, 0, DigestState{}
	if d.h == nil {
		d.h = xxhash.New()
	} else {
		d.h.Reset()
	}
}

// add hashes p, the next bytes that crossed conn. Bytes of any other
// connection are ignored: a write that raced a reconnect.
func (d *streamDigest) add(conn network.Transport, p []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.addLocked(conn, p)
}

func (d *streamDigest) addLocked(conn network.Transport, p []byte) {
	if d.h == nil || conn != d.conn {
		return
	}
	for len(p) > 0 {
		toNext := CheckpointBase - d.n%CheckpointBase
		k := uint64(len(p))
		if k > toNext {
			k = toNext
		}
		_, _ = d.h.Write(p[:k]) //nolint:errcheck // xxhash never fails
		d.n += k
		p = p[k:]
		if d.n%CheckpointBase == 0 {
			d.checkpoint()
		}
	}
}

func (d *streamDigest) checkpoint() {
	cp := Checkpoint{Offset: d.n, Digest: d.h.Sum64()}
	if d.state.Epoch == 0 {
		d.state.Epoch = cp.Digest
	}
	for m := 0; m < CheckpointLevels; m++ {
		if d.n%(CheckpointBase<<(4*m)) == 0 {
			d.state.Latest[m] = cp
		}
	}
}

// snapshot returns the current state.
func (d *streamDigest) snapshot() DigestState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state
}

// write writes p to conn and hashes what was written, in the order writes
// reach the conn.
func (d *streamDigest) write(conn network.Transport, p []byte) (int, error) {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	n, err := conn.Write(p)
	if n > 0 {
		d.add(conn, p[:n])
	}
	return n, err
}

// Checkpoints returns the stream checkpoints of the transport's current conn,
// for what it sent and what it received.
func (mt *ManagedTransport) Checkpoints() (sent, recv DigestState) {
	return mt.sendDig.snapshot(), mt.recvDig.snapshot()
}
