package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/httputil"

	"github.com/skycoin/skywire/pkg/snet"
)

func makeEntry(pk1, pk2 cipher.PubKey, tpType string) Entry {
	return Entry{
		ID:     MakeTransportID(pk1, pk2, tpType),
		Edges:  SortEdges(pk1, pk2),
		Type:   tpType,
		Public: true,
	}
}

func makeEntryFromTpConn(conn *snet.Conn) Entry {
	return makeEntry(conn.LocalPK(), conn.RemotePK(), conn.Network())
}

func compareEntries(expected, received *Entry) error {
	if expected.ID != received.ID {
		return errors.New("received entry's 'tp_id' is not of expected")
	}

	if expected.Edges != received.Edges {
		return errors.New("received entry's 'edges' is not of expected")
	}

	if expected.Type != received.Type {
		return errors.New("received entry's 'type' is not of expected")
	}

	if expected.Public != received.Public {
		return errors.New("received entry's 'public' is not of expected")
	}

	return nil
}

func receiveAndVerifyEntry(r io.Reader, expected *Entry, remotePK cipher.PubKey) (*SignedEntry, error) {
	var recvSE SignedEntry

	if err := json.NewDecoder(r).Decode(&recvSE); err != nil {
		return nil, fmt.Errorf("failed to read entry: %w", err)
	}

	if err := compareEntries(expected, recvSE.Entry); err != nil {
		return nil, err
	}

	sig, err := recvSE.Signature(remotePK)
	if err != nil {
		return nil, fmt.Errorf("invalid remote signature: %w", err)
	}

	if err := cipher.VerifyPubKeySignedPayload(remotePK, sig, recvSE.Entry.ToBinary()); err != nil {
		return nil, err
	}

	return &recvSE, nil
}

// SettlementHS represents a settlement handshake.
// This is the handshake responsible for registering a transport to transport discovery.
type SettlementHS func(ctx context.Context, dc DiscoveryClient, conn *snet.Conn, sk cipher.SecKey) error

// Do performs the settlement handshake.
func (hs SettlementHS) Do(ctx context.Context, dc DiscoveryClient, conn *snet.Conn, sk cipher.SecKey) (err error) {
	done := make(chan struct{})
	go func() {
		err = hs(ctx, dc, conn, sk)
		close(done)
	}()
	select {
	case <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// MakeSettlementHS creates a settlement handshake.
// `init` determines whether the local side is initiating or responding.
// The handshake logic only REGISTERS the transport, and does not update the status of the transport.
func MakeSettlementHS(init bool) SettlementHS {
	// initiating logic.
	initHS := func(ctx context.Context, dc DiscoveryClient, conn *snet.Conn, sk cipher.SecKey) (err error) {
		entry := makeEntryFromTpConn(conn)

		// TODO(evanlinjin): Probably not needed as this is called in mTp already. Need to double check.
		//defer func() {
		//	// @evanlinjin: I used background context to ensure status is always updated.
		//	if _, err := dc.UpdateStatuses(context.Background(), &Status{ID: entry.ID, IsUp: err == nil}); err != nil {
		//		log.WithError(err).Error("Failed to update statuses")
		//	}
		//}()

		// create signed entry and send it to responding visor.
		se, err := NewSignedEntry(&entry, conn.LocalPK(), sk)
		if err != nil {
			return fmt.Errorf("failed to sign entry: %w", err)
		}
		if err := json.NewEncoder(conn).Encode(se); err != nil {
			return fmt.Errorf("failed to write entry: %w", err)
		}

		// await okay signal.
		accepted := make([]byte, 1)
		if _, err := io.ReadFull(conn, accepted); err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}
		if accepted[0] == 0 {
			return fmt.Errorf("transport settlement rejected by remote")
		}
		return nil
	}

	// responding logic.
	respHS := func(ctx context.Context, dc DiscoveryClient, conn *snet.Conn, sk cipher.SecKey) error {
		entry := makeEntryFromTpConn(conn)

		// receive, verify and sign entry.
		recvSE, err := receiveAndVerifyEntry(conn, &entry, conn.RemotePK())
		if err != nil {
			return err
		}

		if err := recvSE.Sign(conn.LocalPK(), sk); err != nil {
			return fmt.Errorf("failed to sign received entry: %w", err)
		}

		entry = *recvSE.Entry

		// Ensure transport is registered.
		if err := dc.RegisterTransports(ctx, recvSE); err != nil {
			if httpErr, ok := err.(*httputil.HTTPError); ok && httpErr.Status == http.StatusConflict {
				log.WithError(err).Debug("An expected error occurred while trying to register transport.")
			} else {
				// TODO(evanlinjin): In the future, this should return error and result in failed HS.
				log.WithError(err).Error("Failed to register transport.")
			}
		}

		// inform initiating visor.
		if _, err := conn.Write([]byte{1}); err != nil {
			return fmt.Errorf("failed to accept transport settlement: write failed: %w", err)
		}
		return nil
	}

	if init {
		return initHS
	}
	return respHS
}
