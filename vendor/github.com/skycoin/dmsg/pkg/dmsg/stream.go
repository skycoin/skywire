package dmsg

import (
	"context"
	"net"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/yamux"

	"github.com/skycoin/dmsg/pkg/noise"
)

// Stream represents a dmsg connection between two dmsg clients.
type Stream struct {
	ses  *ClientSession // back reference
	yStr *yamux.Stream

	// The following fields are to be filled after handshake.
	lAddr  Addr
	rAddr  Addr
	ns     *noise.Noise
	nsConn *noise.ReadWriter
	close  func() // to be called when closing
	log    logrus.FieldLogger
}

func newInitiatingStream(cSes *ClientSession) (*Stream, error) {
	yStr, err := cSes.ys.OpenStream()
	if err != nil {
		return nil, err
	}
	return &Stream{ses: cSes, yStr: yStr}, nil
}

func newRespondingStream(cSes *ClientSession) (*Stream, error) {
	yStr, err := cSes.ys.AcceptStream()
	if err != nil {
		return nil, err
	}
	return &Stream{ses: cSes, yStr: yStr}, nil
}

// Close closes the dmsg stream.
func (s *Stream) Close() error {
	if s == nil {
		return nil
	}
	if s.close != nil {
		s.close()
	}
	return s.yStr.Close()
}

// Logger returns the internal logrus.FieldLogger instance.
func (s *Stream) Logger() logrus.FieldLogger {
	return s.log
}

func (s *Stream) writeRequest(rAddr Addr) (req StreamRequest, err error) {
	// Reserve stream in porter.
	var lPort uint16
	if lPort, s.close, err = s.ses.porter.ReserveEphemeral(context.Background(), s); err != nil {
		return
	}

	// Prepare fields.
	s.prepareFields(true, Addr{PK: s.ses.LocalPK(), Port: lPort}, rAddr)

	// Prepare request.
	var nsMsg []byte
	if nsMsg, err = s.ns.MakeHandshakeMessage(); err != nil {
		return
	}
	req = StreamRequest{
		Timestamp: time.Now().UnixNano(),
		SrcAddr:   s.lAddr,
		DstAddr:   s.rAddr,
		NoiseMsg:  nsMsg,
	}
	obj := MakeSignedStreamRequest(&req, s.ses.localSK())

	// Write request.
	err = s.ses.writeObject(s.yStr, obj)
	return
}

func (s *Stream) readRequest() (req StreamRequest, err error) {
	var obj SignedObject
	if obj, err = s.ses.readObject(s.yStr); err != nil {
		return
	}
	if req, err = obj.ObtainStreamRequest(); err != nil {
		return
	}
	if err = req.Verify(0); err != nil {
		return
	}
	if req.DstAddr.PK != s.ses.LocalPK() {
		err = ErrReqInvalidDstPK
		return
	}

	// Prepare fields.
	s.prepareFields(false, req.DstAddr, req.SrcAddr)

	if err = s.ns.ProcessHandshakeMessage(req.NoiseMsg); err != nil {
		return
	}
	return
}

func (s *Stream) writeResponse(reqHash cipher.SHA256) error {
	// Obtain associated local listener.
	pVal, ok := s.ses.porter.PortValue(s.lAddr.Port)
	if !ok {
		return ErrReqNoListener
	}
	lis, ok := pVal.(*Listener)
	if !ok {
		return ErrReqNoListener
	}

	// Prepare and write response.
	nsMsg, err := s.ns.MakeHandshakeMessage()
	if err != nil {
		return err
	}
	resp := StreamResponse{
		ReqHash:  reqHash,
		Accepted: true,
		NoiseMsg: nsMsg,
	}
	obj := MakeSignedStreamResponse(&resp, s.ses.localSK())

	if err := s.ses.writeObject(s.yStr, obj); err != nil {
		return err
	}

	// Push stream to listener.
	return lis.introduceStream(s)
}

func (s *Stream) readResponse(req StreamRequest) error {
	obj, err := s.ses.readObject(s.yStr)
	if err != nil {
		return err
	}
	resp, err := obj.ObtainStreamResponse()
	if err != nil {
		return err
	}
	if err := resp.Verify(req); err != nil {
		return err
	}
	return s.ns.ProcessHandshakeMessage(resp.NoiseMsg)
}

func (s *Stream) prepareFields(init bool, lAddr, rAddr Addr) {
	ns, err := noise.New(noise.HandshakeKK, noise.Config{
		LocalPK:   s.ses.LocalPK(),
		LocalSK:   s.ses.localSK(),
		RemotePK:  rAddr.PK,
		Initiator: init,
	})
	if err != nil {
		s.log.WithError(err).Panic("Failed to prepare stream noise object.")
	}

	s.lAddr = lAddr
	s.rAddr = rAddr
	s.ns = ns
	s.nsConn = noise.NewReadWriter(s.yStr, s.ns)
	s.log = s.ses.log.WithField("stream", s.lAddr.ShortString()+"->"+s.rAddr.ShortString())
}

// LocalAddr returns the local address of the dmsg stream.
func (s *Stream) LocalAddr() net.Addr {
	return s.lAddr
}

// RawLocalAddr returns the local address as dmsg.Addr type.
func (s *Stream) RawLocalAddr() Addr {
	return s.lAddr
}

// RemoteAddr returns the remote address of the dmsg stream.
func (s *Stream) RemoteAddr() net.Addr {
	return s.rAddr
}

// RawRemoteAddr returns the remote address as dmsg.Addr type.
func (s *Stream) RawRemoteAddr() Addr {
	return s.rAddr
}

// ServerPK returns the remote PK of the dmsg.Server used to relay frames to and from the remote client.
func (s *Stream) ServerPK() cipher.PubKey {
	return s.ses.RemotePK()
}

// StreamID returns the stream ID.
func (s *Stream) StreamID() uint32 {
	return s.yStr.StreamID()
}

// Read implements io.Reader
func (s *Stream) Read(b []byte) (int, error) {
	return s.nsConn.Read(b)
}

// Write implements io.Writer
func (s *Stream) Write(b []byte) (int, error) {
	return s.nsConn.Write(b)
}

// SetDeadline implements net.Conn
func (s *Stream) SetDeadline(t time.Time) error {
	return s.yStr.SetDeadline(t)
}

// SetReadDeadline implements net.Conn
func (s *Stream) SetReadDeadline(t time.Time) error {
	return s.yStr.SetReadDeadline(t)
}

// SetWriteDeadline implements net.Conn
func (s *Stream) SetWriteDeadline(t time.Time) error {
	return s.yStr.SetWriteDeadline(t)
}
