// Package dmsg pkg/dmsg/server_session.go
package dmsg

import (
	"fmt"
	"io"
	"net"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/xtaci/smux"

	"github.com/skycoin/dmsg/pkg/dmsg/metrics"
	"github.com/skycoin/dmsg/pkg/noise"
)

const (
	// maxConcurrentStreams limits how many streams can be served concurrently
	// per session to prevent a single session from exhausting server resources.
	maxConcurrentStreams = 2048

	// streamErrorBackoff is the delay after a non-fatal stream accept error
	// to prevent CPU spin on persistent errors.
	streamErrorBackoff = 50 * time.Millisecond
)

// ServerSession represents a session from the perspective of a dmsg server.
type ServerSession struct {
	*SessionCommon
	m metrics.Metrics
}

func makeServerSession(m metrics.Metrics, entity *EntityCommon, conn net.Conn) (ServerSession, error) {
	var sSes ServerSession
	sSes.SessionCommon = new(SessionCommon)
	sSes.nMap = make(noise.NonceMap)
	if err := sSes.SessionCommon.initServer(entity, conn); err != nil {
		m.RecordSession(metrics.DeltaFailed) // record failed connection
		return sSes, err
	}
	sSes.m = m
	return sSes, nil
}

// Close implements io.Closer
func (ss *ServerSession) Close() error {
	if ss == nil {
		return nil
	}
	return ss.SessionCommon.Close()
}

// Serve serves the session.
func (ss *ServerSession) Serve() {
	ss.m.RecordSession(metrics.DeltaConnect)          // record successful connection
	defer ss.m.RecordSession(metrics.DeltaDisconnect) // record disconnection

	// Semaphore to limit concurrent streams per session.
	sem := make(chan struct{}, maxConcurrentStreams)

	if ss.sm.smux != nil {
		for {
			sStr, err := ss.sm.smux.AcceptStream()
			if err != nil {
				if err == io.EOF || err == smux.ErrInvalidProtocol || ss.sm.smux.IsClosed() {
					ss.log.WithError(err).Info("Stopping session...")
					return
				}
				ss.log.WithError(err).Warn("Failed to accept smux stream, continuing...")
				time.Sleep(streamErrorBackoff)
				continue
			}

			log := ss.log.WithField("smux_id", sStr.ID())

			// Acquire semaphore slot; if full, reject the stream.
			select {
			case sem <- struct{}{}:
			default:
				log.Warn("Max concurrent streams reached, rejecting stream.")
				sStr.Close() //nolint:errcheck,gosec
				continue
			}

			log.Info("Initiating stream.")
			go func(sStr *smux.Stream) {
				defer func() { <-sem }()
				defer func() {
					if r := recover(); r != nil {
						log.WithField("panic", r).Error("Recovered from panic in serveStream")
					}
				}()
				err := ss.serveStream(log, sStr, ss.sm.addr)
				log.WithError(err).Info("Stopped stream.")
			}(sStr)
		}
	} else {
		for {
			yStr, err := ss.sm.yamux.AcceptStream()
			if err != nil {
				if err == yamux.ErrSessionShutdown || err == io.EOF || ss.sm.yamux.IsClosed() {
					ss.log.WithError(err).Info("Stopping session...")
					return
				}
				ss.log.WithError(err).Warn("Failed to accept yamux stream, continuing...")
				time.Sleep(streamErrorBackoff)
				continue
			}

			log := ss.log.WithField("yamux_id", yStr.StreamID())

			// Acquire semaphore slot; if full, reject the stream.
			select {
			case sem <- struct{}{}:
			default:
				log.Warn("Max concurrent streams reached, rejecting stream.")
				yStr.Close() //nolint:errcheck,gosec
				continue
			}

			log.Info("Initiating stream.")
			go func(yStr *yamux.Stream) {
				defer func() { <-sem }()
				defer func() {
					if r := recover(); r != nil {
						log.WithField("panic", r).Error("Recovered from panic in serveStream")
					}
				}()
				err := ss.serveStream(log, yStr, ss.sm.addr)
				log.WithError(err).Info("Stopped stream.")
			}(yStr)
		}
	}
}

// struct

func (ss *ServerSession) serveStream(log logrus.FieldLogger, yStr io.ReadWriteCloser, addr net.Addr) error {
	// Set a deadline for the initial stream request read so a slow or
	// malicious client cannot hold a goroutine and semaphore slot indefinitely.
	if conn, ok := yStr.(net.Conn); ok {
		if err := conn.SetReadDeadline(time.Now().Add(HandshakeTimeout)); err != nil {
			return fmt.Errorf("set read deadline: %w", err)
		}
	}

	readRequest := func() (StreamRequest, error) {
		obj, err := ss.readObject(yStr)
		if err != nil {
			return StreamRequest{}, err
		}
		req, err := obj.ObtainStreamRequest()
		if err != nil {
			return StreamRequest{}, err
		}
		// TODO(evanlinjin): Implement timestamp tracker.
		if err := req.Verify(0); err != nil {
			return StreamRequest{}, err
		}
		if req.SrcAddr.PK != ss.rPK {
			return StreamRequest{}, ErrReqInvalidSrcPK
		}
		return req, nil
	}

	// Read request.
	req, err := readRequest()
	if err != nil {
		ss.m.RecordStream(metrics.DeltaFailed) // record failed stream
		return err
	}

	log = log.
		WithField("src_addr", req.SrcAddr).
		WithField("dst_addr", req.DstAddr)

	log.Debug("Read stream request from initiating side.")
	if req.IPinfo && req.DstAddr.PK == ss.entity.LocalPK() {
		log.Debug("Received IP stream request.")

		ip, err := addrToIP(addr)
		if err != nil {
			ss.m.RecordStream(metrics.DeltaFailed) // record failed stream
			return err
		}

		resp := StreamResponse{
			ReqHash:  req.raw.Hash(),
			Accepted: true,
			IP:       ip,
		}
		obj, err := MakeSignedStreamResponse(&resp, ss.entity.LocalSK())
		if err != nil {
			ss.m.RecordStream(metrics.DeltaFailed) // record failed stream
			return err
		}

		if err := ss.writeObject(yStr, obj); err != nil {
			ss.m.RecordStream(metrics.DeltaFailed) // record failed stream
			return err
		}
		log.Debug("Wrote IP stream response.")
		return nil
	}

	// Obtain next session.
	ss2, ok := ss.entity.serverSession(req.DstAddr.PK)
	if !ok {
		ss.m.RecordStream(metrics.DeltaFailed) // record failed stream
		return ErrReqNoNextSession
	}
	log.Debug("Obtained next session.")

	// Forward request and obtain/check response.
	yStr2, resp, err := ss2.forwardRequest(req)
	if err != nil {
		ss.m.RecordStream(metrics.DeltaFailed) // record failed stream
		return err
	}
	log.Debug("Forwarded stream request.")

	// Forward response.
	if err := ss.writeObject(yStr, resp); err != nil {
		ss.m.RecordStream(metrics.DeltaFailed) // record failed stream
		return err
	}
	log.Debug("Forwarded stream response.")

	// Clear the read deadline before the long-lived bidirectional copy.
	if conn, ok := yStr.(net.Conn); ok {
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
	}

	// Serve stream.
	log.Info("Serving stream.")
	ss.m.RecordStream(metrics.DeltaConnect)          // record successful stream
	defer ss.m.RecordStream(metrics.DeltaDisconnect) // record disconnection
	return netutil.CopyReadWriteCloser(yStr, yStr2)
}

func addrToIP(addr net.Addr) (net.IP, error) {
	switch a := addr.(type) {
	case *net.TCPAddr:
		return a.IP, nil
	case *net.UDPAddr:
		return a.IP, nil
	default:
		return nil, fmt.Errorf("unsupported address type %T", addr)
	}
}

func (ss *ServerSession) forwardRequest(req StreamRequest) (mStr io.ReadWriteCloser, respObj SignedObject, err error) {
	defer func() {
		if err != nil && mStr != nil {
			ss.log.
				WithError(mStr.Close()).
				Debugf("After forwardRequest failed, the yamux stream is closed.")
		}
	}()
	if ss.sm.smux != nil {
		if mStr, err = ss.sm.smux.OpenStream(); err != nil {
			return nil, nil, err
		}
	} else {
		if mStr, err = ss.sm.yamux.OpenStream(); err != nil {
			return nil, nil, err
		}
	}
	if err = ss.writeObject(mStr, req.raw); err != nil {
		return nil, nil, err
	}
	if respObj, err = ss.readObject(mStr); err != nil {
		return nil, nil, err
	}
	var resp StreamResponse
	if resp, err = respObj.ObtainStreamResponse(); err != nil {
		return nil, nil, err
	}
	if err = resp.Verify(req); err != nil {
		return nil, nil, err
	}
	return mStr, respObj, nil
}
