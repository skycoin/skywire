// Package dmsg pkg/dmsg/client_session.go
package dmsg

import (
	"errors"
	"net"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
)

// ClientSession represents a session from the perspective of a dmsg client.
type ClientSession struct {
	*SessionCommon
	porter *netutil.Porter
}

func makeClientSession(entity *EntityCommon, porter *netutil.Porter, conn net.Conn, rPK cipher.PubKey) (ClientSession, error) {
	var cSes ClientSession
	cSes.SessionCommon = new(SessionCommon)
	if err := cSes.SessionCommon.initClient(entity, conn, rPK); err != nil {
		return cSes, err
	}
	cSes.porter = porter
	return cSes, nil
}

// DialStream attempts to dial a stream to a remote client via the dmsg server that this session is connected to.
func (cs *ClientSession) DialStream(dst Addr) (dStr *Stream, err error) {
	log := cs.log.
		WithField("func", "ClientSession.DialStream").
		WithField("dst_addr", dst)

	if dStr, err = newInitiatingStream(cs); err != nil {
		return nil, err
	}

	// Close stream on failure.
	defer func() {
		if err != nil {
			log.WithError(err).
				WithField("close_error", dStr.Close()).
				Debug("Stream closed on failure.")
		}
	}()

	// Prepare deadline.
	if err = dStr.SetDeadline(time.Now().Add(HandshakeTimeout)); err != nil {
		return nil, err
	}

	// Do stream handshake.
	req, err := dStr.writeRequest(dst)
	if err != nil {
		return nil, err
	}

	if err := dStr.readResponse(req); err != nil {
		return nil, err
	}

	// Clear deadline.
	if err = dStr.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}

	return dStr, err
}

// serve accepts incoming streams from remote clients.
func (cs *ClientSession) serve() error {
	defer func() {
		if err := cs.Close(); err != nil {
			cs.log.WithError(err).
				Debug("On (*ClientSession).serve() return, close client session resulted in error.")
		}
	}()
	for {
		if _, err := cs.acceptStream(); err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Temporary() { //nolint
				cs.log.
					WithError(err).
					Debug("Failed to accept stream.")
				continue
			}

			if errors.Is(err, yamux.ErrSessionShutdown) {
				cs.log.WithError(err).Debug("Stopped accepting streams.")
				return err
			}
			cs.log.WithError(err).Warn("Stopped accepting streams.")
			return err
		}
	}
}

func (cs *ClientSession) acceptStream() (dStr *Stream, err error) {
	if dStr, err = newRespondingStream(cs); err != nil {
		return nil, err
	}

	// Close stream on failure.
	defer func() {
		if err != nil {
			if scErr := dStr.Close(); scErr != nil {
				cs.log.WithError(scErr).
					Debug("On (*ClientSession).acceptStream() failure, close stream resulted in error.")
			}
		}
	}()

	// Prepare deadline.
	if err = dStr.SetDeadline(time.Now().Add(HandshakeTimeout)); err != nil {
		return nil, err
	}

	// Do stream handshake.
	req, err := dStr.readRequest()
	if err != nil {
		return nil, err
	}
	if err = dStr.writeResponse(req.raw.Hash()); err != nil {
		return nil, err
	}

	// Clear deadline.
	if err = dStr.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}

	return dStr, err
}
