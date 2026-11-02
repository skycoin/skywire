// Package dmsgasync pkg/dmsg/dmsgasync/client.go c1-net-dmsg
package dmsgasync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// Port is the dmsg port inbox hosts serve.
const Port = skyenv.DmsgAsyncPort

// Dialer opens a dmsg stream to pk on port.
type Dialer func(ctx context.Context, pk cipher.PubKey, port uint16) (net.Conn, error)

// NewRef returns a fresh deposit reference. A sender reuses one ref for every
// retry of the same message so a host stores it once.
func NewRef() string {
	var b [refLen]byte
	_, _ = rand.Read(b[:]) //nolint:errcheck // crypto/rand does not fail
	return hex.EncodeToString(b[:])
}

// do opens a stream to host, sends req and reads the first reply.
func do(ctx context.Context, dial Dialer, host cipher.PubKey, req request) (net.Conn, reply, error) {
	conn, err := dial(ctx, host, Port)
	if err != nil {
		return nil, reply{}, err
	}
	deadline := time.Now().Add(opTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline) //nolint:errcheck
	if err := writeJSON(conn, req); err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, reply{}, err
	}
	var rep reply
	if err := readJSON(conn, &rep); err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, reply{}, err
	}
	return conn, rep, nil
}

func once(ctx context.Context, dial Dialer, host cipher.PubKey, req request) (reply, error) {
	conn, rep, err := do(ctx, dial, host, req)
	if err != nil {
		return reply{}, err
	}
	_ = conn.Close() //nolint:errcheck
	return rep, rep.err()
}

// Register registers rec and pol with host. rec MUST be signed by the
// dialer's own key and name host.
func Register(ctx context.Context, dial Dialer, host cipher.PubKey, rec Record, pol Policy) error {
	_, err := once(ctx, dial, host, request{Op: OpRegister, Record: &rec, Policy: &pol})
	return err
}

// Unregister removes the dialer's registration from host and drops what
// waits for it there.
func Unregister(ctx context.Context, dial Dialer, host cipher.PubKey) (int, error) {
	rep, err := once(ctx, dial, host, request{Op: OpUnregister})
	return rep.Dropped, err
}

// LookupRecord asks host for pk's current record and verifies it.
func LookupRecord(ctx context.Context, dial Dialer, host, pk cipher.PubKey) (Record, error) {
	rep, err := once(ctx, dial, host, request{Op: OpRecord, PK: pk})
	if err != nil {
		return Record{}, err
	}
	if rep.Record == nil || rep.Record.PK != pk {
		return Record{}, ErrBadRecord
	}
	return *rep.Record, rep.Record.Verify(time.Now())
}

// Deposit leaves payload for `to` on port with host and returns its id.
func Deposit(ctx context.Context, dial Dialer, host, to cipher.PubKey, port uint16, ref string, payload []byte) (string, error) {
	conn, rep, err := do(ctx, dial, host, request{Op: OpDeposit, To: to, Port: port, Size: len(payload), Ref: ref})
	if err != nil {
		return "", err
	}
	defer conn.Close() //nolint:errcheck
	if err := rep.err(); err != nil {
		return "", err
	}
	if err := writeFrame(conn, payload); err != nil {
		return "", err
	}
	var final reply
	if err := readJSON(conn, &final); err != nil {
		return "", err
	}
	return final.ID, final.err()
}

// DepositAny deposits on rec's hosts in order until one takes it, and
// returns that host. It fails with the last error when none does.
func DepositAny(ctx context.Context, dial Dialer, rec Record, port uint16, ref string, payload []byte) (cipher.PubKey, error) {
	if err := rec.Verify(time.Now()); err != nil {
		return cipher.PubKey{}, err
	}
	if len(rec.Hosts) == 0 {
		return cipher.PubKey{}, ErrNotHosted
	}
	var last error
	for _, host := range rec.Hosts {
		if _, err := Deposit(ctx, dial, host, rec.PK, port, ref, payload); err != nil {
			last = fmt.Errorf("%s: %w", host, err)
			if errors.Is(err, ErrNotAllowed) || errors.Is(err, ErrTooBig) {
				// Every host applies the same policy and size.
				return cipher.PubKey{}, last
			}
			continue
		}
		return host, nil
	}
	return cipher.PubKey{}, last
}

// Handler takes one fetched message. A nil error acks it. An error leaves it
// on the host for a later fetch.
type Handler func(Message) error

// Fetch takes what waits for the dialer on host for ports, hands each message
// to handle and acks the ones it took. It repeats while the host has more and
// reports how many were handled.
func Fetch(ctx context.Context, dial Dialer, host cipher.PubKey, ports []uint16, handle Handler) (int, error) {
	total := 0
	for {
		n, more, err := fetchBatch(ctx, dial, host, ports, handle)
		total += n
		if err != nil || !more || n == 0 {
			return total, err
		}
	}
}

func fetchBatch(ctx context.Context, dial Dialer, host cipher.PubKey, ports []uint16, handle Handler) (int, bool, error) {
	conn, rep, err := do(ctx, dial, host, request{Op: OpFetch, Ports: ports})
	if err != nil {
		return 0, false, err
	}
	if err := rep.err(); err != nil {
		_ = conn.Close() //nolint:errcheck
		return 0, false, err
	}
	msgs := make([]Message, 0, len(rep.Messages))
	for _, hd := range rep.Messages {
		payload, err := readFrame(conn, fetchBatchBytes)
		if err != nil {
			_ = conn.Close() //nolint:errcheck
			return 0, false, err
		}
		if len(payload) != hd.Size {
			_ = conn.Close() //nolint:errcheck
			return 0, false, ErrBadRequest
		}
		msgs = append(msgs, Message{Header: hd, Payload: payload, Host: host})
	}
	_ = conn.Close() //nolint:errcheck
	var acks []string
	for _, m := range msgs {
		if handle(m) == nil {
			acks = append(acks, m.ID)
		}
	}
	if len(acks) == 0 {
		return 0, rep.More, nil
	}
	if _, err := once(ctx, dial, host, request{Op: OpAck, IDs: acks}); err != nil {
		return len(acks), false, err
	}
	return len(acks), rep.More, nil
}
