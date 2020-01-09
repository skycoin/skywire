package dmsg

import (
	"io"
	"net"

	"github.com/SkycoinProject/yamux"

	"github.com/SkycoinProject/dmsg/netutil"
	"github.com/SkycoinProject/dmsg/noise"
)

// ServerSession represents a session from the perspective of a dmsg server.
type ServerSession struct {
	*SessionCommon
}

func makeServerSession(entity *EntityCommon, conn net.Conn) (ServerSession, error) {
	var sSes ServerSession
	sSes.SessionCommon = new(SessionCommon)
	sSes.nMap = make(noise.NonceMap)
	if err := sSes.SessionCommon.initServer(entity, conn); err != nil {
		return sSes, err
	}
	return sSes, nil
}

// Close implements io.Closer
func (ss *ServerSession) Close() (err error) {
	if ss != nil {
		if ss.SessionCommon != nil {
			err = ss.SessionCommon.Close()
		}
		ss.rMx.Lock()
		ss.nMap = nil
		ss.rMx.Unlock()
	}
	return err
}

// Serve serves the session.
func (ss *ServerSession) Serve() {
	for {
		yStr, err := ss.ys.AcceptStream()
		if err != nil {
			switch err {
			case yamux.ErrSessionShutdown, io.EOF:
				ss.log.WithError(err).Info("Stopping session...")
			default:
				ss.log.WithError(err).Warn("Failed to accept stream, stopping session...")
			}
			return
		}

		ss.log.Info("Serving stream.")
		go func(yStr *yamux.Stream) {
			err := ss.serveStream(yStr)
			ss.log.WithError(err).Info("Stopped stream.")
		}(yStr)
	}
}

func (ss *ServerSession) serveStream(yStr *yamux.Stream) error {
	readRequest := func() (StreamDialRequest, error) {
		var req StreamDialRequest
		if err := ss.readEncryptedGob(yStr, &req); err != nil {
			return req, err
		}
		if err := req.Verify(0); err != nil { // TODO(evanlinjin): timestamp tracker.
			return req, ErrReqInvalidTimestamp
		}
		if req.SrcAddr.PK != ss.rPK {
			return req, ErrReqInvalidSrcPK
		}
		return req, nil
	}

	// Read request.
	req, err := readRequest()
	if err != nil {
		return err
	}

	// Obtain next session.
	ss2, ok := ss.entity.ServerSession(req.DstAddr.PK)
	if !ok {
		return ErrReqNoSession
	}

	// Forward request and obtain/check response.
	yStr2, resp, err := ss2.forwardRequest(req)
	if err != nil {
		return err
	}

	// Forward response.
	if err := ss.writeEncryptedGob(yStr, resp); err != nil {
		return err
	}

	// Serve stream.
	return netutil.CopyReadWriteCloser(yStr, yStr2)
}

func (ss *ServerSession) forwardRequest(req StreamDialRequest) (*yamux.Stream, StreamDialResponse, error) {
	yStr, err := ss.ys.OpenStream()
	if err != nil {
		return nil, StreamDialResponse{}, err
	}
	if err := ss.writeEncryptedGob(yStr, req); err != nil {
		_ = yStr.Close() //nolint:errcheck
		return nil, StreamDialResponse{}, err
	}
	var resp StreamDialResponse
	if err := ss.readEncryptedGob(yStr, &resp); err != nil {
		_ = yStr.Close() //nolint:errcheck
		return nil, StreamDialResponse{}, err
	}
	if err := resp.Verify(req.DstAddr.PK, req.Hash()); err != nil {
		_ = yStr.Close() //nolint:errcheck
		return nil, StreamDialResponse{}, err
	}
	return yStr, resp, nil
}
