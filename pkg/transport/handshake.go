// Package transport pkg/transport/handshake.go
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network"
)

type hsResponse byte

const (
	responseFailure hsResponse = iota
	responseOK
	responseSignatureErr
	responseInvalidEntry
)

func makeEntryFromTransport(transport network.Transport) Entry {
	aPK, bPK := transport.LocalPK(), transport.RemotePK()
	return MakeEntry(aPK, bPK, transport.Network(), LabelUser)
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

	return nil
}

func receiveAndVerifyEntry(r io.Reader, expected *Entry, remotePK cipher.PubKey) (*SignedEntry, error) {
	var recvSE SignedEntry

	if err := json.NewDecoder(r).Decode(&recvSE); err != nil {
		return nil, fmt.Errorf("failed to read entry: %w", err)
	}

	if recvSE.Entry == nil {
		return nil, fmt.Errorf("failed to read entry: entry part of singed entry is empty")
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
type SettlementHS func(ctx context.Context, dc DiscoveryClient, transport network.Transport, sk cipher.SecKey) error

// Do performs the settlement handshake.
func (hs SettlementHS) Do(ctx context.Context, dc DiscoveryClient, transport network.Transport, sk cipher.SecKey) (err error) {
	done := make(chan struct{})
	go func() {
		err = hs(ctx, dc, transport, sk)
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
func MakeSettlementHS(init bool, log *logging.Logger) SettlementHS {
	// initiating logic.
	initHS := func(ctx context.Context, dc DiscoveryClient, transport network.Transport, sk cipher.SecKey) (err error) {
		entry := makeEntryFromTransport(transport)

		// create signed entry and send it to responding visor.
		se, err := NewSignedEntry(&entry, transport.LocalPK(), sk)
		if err != nil {
			return fmt.Errorf("failed to sign entry: %w", err)
		}
		if err := json.NewEncoder(transport).Encode(se); err != nil {
			return fmt.Errorf("failed to write entry: %w", err)
		}

		// await okay signal.
		accepted := make([]byte, 1)
		if _, err := io.ReadFull(transport, accepted); err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}
		switch hsResponse(accepted[0]) {
		case responseOK:
			return nil
		case responseFailure:
			return fmt.Errorf("transport settlement rejected by remote")
		case responseInvalidEntry:
			return fmt.Errorf("invalid entry")
		case responseSignatureErr:
			return fmt.Errorf("signature error")
		default:
			return fmt.Errorf("invalid remote response")
		}
	}

	// responding logic.
	respHS := func(ctx context.Context, dc DiscoveryClient, transport network.Transport, sk cipher.SecKey) error {
		entry := makeEntryFromTransport(transport)

		// receive, verify and sign entry.
		recvSE, err := receiveAndVerifyEntry(transport, &entry, transport.RemotePK())
		if err != nil {
			writeHsResponse(transport, responseInvalidEntry) //nolint:errcheck, gosec
			return err
		}

		if err := recvSE.Sign(transport.LocalPK(), sk); err != nil {
			writeHsResponse(transport, responseSignatureErr) //nolint:errcheck, gosec
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
		return writeHsResponse(transport, responseOK)
	}

	if init {
		return initHS
	}
	return respHS
}

func writeHsResponse(w io.Writer, response hsResponse) error {
	if _, err := w.Write([]byte{byte(response)}); err != nil {
		return fmt.Errorf("failed to accept transport settlement: write failed: %w", err)
	}
	return nil
}
