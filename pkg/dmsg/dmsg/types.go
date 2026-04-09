// Package dmsg pkg/dmsg/types.go
package dmsg

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/xtaci/smux"
)

const (
	// Type returns the stream type string.
	Type = "dmsg"

	// HandshakePayloadVersion contains payload version to maintain compatibility with future versions
	// of HandshakeData format.
	HandshakePayloadVersion = "2.0"
)

var (
	// DialTimeout defines the duration a TCP dial to a dmsg server should take.
	// This prevents blocking for minutes on unresponsive/overloaded servers.
	DialTimeout = 10 * time.Second

	// HandshakeTimeout defines the duration a stream handshake should take.
	// On a healthy network the noise handshake completes in milliseconds.
	// 5s is generous — anything longer means the destination is unreachable.
	HandshakeTimeout = 5 * time.Second

	// StreamIdleTimeout defines how long a stream can be idle (no reads)
	// before it is considered stale and closed. This prevents streams stuck
	// in waitRead from holding ephemeral ports indefinitely.
	StreamIdleTimeout = 2 * time.Minute

	// AcceptBufferSize defines the size of the accepts buffer.
	AcceptBufferSize = 20
)

// YamuxConfig returns a tuned yamux configuration for dmsg sessions.
func YamuxConfig() *yamux.Config {
	return &yamux.Config{
		AcceptBacklog:          512,
		EnableKeepAlive:        true,
		KeepAliveInterval:      30 * time.Second,
		ConnectionWriteTimeout: 10 * time.Second,
		MaxStreamWindowSize:    256 * 1024,
		StreamOpenTimeout:      20 * time.Second,
		StreamCloseTimeout:     30 * time.Second,
		LogOutput:              io.Discard,
	}
}

// SmuxConfig returns a tuned smux configuration for dmsg sessions.
func SmuxConfig() *smux.Config {
	return &smux.Config{
		Version:           2,
		KeepAliveInterval: 10 * time.Second,
		KeepAliveTimeout:  30 * time.Second,
		MaxFrameSize:      32768,
		MaxReceiveBuffer:  1048576,
		MaxStreamBuffer:   65536,
	}
}

// Addr implements net.Addr for dmsg addresses.
type Addr struct {
	PK   cipher.PubKey `json:"public_key"`
	Port uint16        `json:"port"`
}

// Network returns "dmsg"
func (Addr) Network() string {
	return Type
}

// String returns public key and port of node split by colon.
func (a Addr) String() string {
	if a.Port == 0 {
		return fmt.Sprintf("%s:~", a.PK)
	}
	return fmt.Sprintf("%s:%d", a.PK, a.Port)
}

// ShortString returns a shortened string representation of the address.
func (a Addr) ShortString() string {
	const PKLen = 8
	if a.Port == 0 {
		return fmt.Sprintf("%s:~", a.PK.String()[:PKLen])
	}
	return fmt.Sprintf("%s:%d", a.PK.String()[:PKLen], a.Port)
}

// Set implements pflag.Value for Addr.
func (a *Addr) Set(s string) error {
	parts := strings.Split(s, ":")
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
	}
	switch len(parts) {
	case 0:
		a.PK = cipher.PubKey{}
		a.Port = 0
		return nil
	case 1:
		return a.PK.Set(parts[0])
	case 2:
		if parts[0] == "" {
			a.PK = cipher.PubKey{}
		} else {
			if err := a.PK.Set(parts[0]); err != nil {
				return err
			}
		}
		if parts[1] == "~" || parts[1] == "" {
			a.Port = 0
		} else {
			_, err := fmt.Sscan(parts[1], &a.Port)
			return err
		}
		return nil
	default:
		return errors.New("invalid dmsg.Addr string")
	}
}

// Type implements pflag.Value for Addr.
func (Addr) Type() string {
	return "dmsg.Addr"
}

/* Request & Response */

const sigLen = len(cipher.Sig{})

// SignedObject represents a gob-encoded structure prepended with a signature.
type SignedObject []byte

// MakeSignedStreamRequest encodes and signs a StreamRequest into a SignedObject format.
func MakeSignedStreamRequest(req *StreamRequest, sk cipher.SecKey) (SignedObject, error) {
	obj, err := encodeGob(req)
	if err != nil {
		return nil, fmt.Errorf("dmsg: encode stream request: %w", err)
	}
	sig, err := SignBytes(obj, sk)
	if err != nil {
		return nil, err
	}
	signedObj := append(sig[:], obj...)
	req.raw = signedObj
	return signedObj, nil
}

// MakeSignedStreamResponse encodes and signs a StreamResponse into a SignedObject format.
func MakeSignedStreamResponse(resp *StreamResponse, sk cipher.SecKey) (SignedObject, error) {
	obj, err := encodeGob(resp)
	if err != nil {
		return nil, fmt.Errorf("dmsg: encode stream response: %w", err)
	}
	sig, err := SignBytes(obj, sk)
	if err != nil {
		return nil, err
	}
	signedObj := append(sig[:], obj...)
	resp.raw = signedObj
	return signedObj, nil
}

// Valid returns true if the SignedObject has a valid length.
func (so SignedObject) Valid() bool {
	return len(so) > sigLen
}

// Hash returns the hash of the SignedObject.
func (so SignedObject) Hash() cipher.SHA256 {
	return cipher.SumSHA256(so)
}

// Sig returns the prepended signature section of the SignedObject.
func (so SignedObject) Sig() cipher.Sig {
	var sig cipher.Sig
	copy(sig[:], so)
	return sig
}

// Object returns the bytes of the SignedObject that contain the encoded object.
func (so SignedObject) Object() []byte {
	return so[sigLen:]
}

// ObtainStreamRequest obtains a StreamRequest from the encoded object bytes.
func (so SignedObject) ObtainStreamRequest() (StreamRequest, error) {
	if !so.Valid() {
		return StreamRequest{}, ErrSignedObjectInvalid
	}
	var req StreamRequest
	err := decodeGob(&req, so[sigLen:])
	req.raw = so
	return req, err
}

// ObtainStreamResponse obtains a StreamResponse from the encoded object bytes.
func (so SignedObject) ObtainStreamResponse() (StreamResponse, error) {
	if !so.Valid() {
		return StreamResponse{}, ErrSignedObjectInvalid
	}
	var resp StreamResponse
	err := decodeGob(&resp, so[sigLen:])
	resp.raw = so
	return resp, err
}

// StreamRequest represents a stream dial request object.
type StreamRequest struct {
	Timestamp int64
	SrcAddr   Addr
	DstAddr   Addr
	IPinfo    bool
	NoiseMsg  []byte

	raw SignedObject `enc:"-"` // back reference.
}

// Verify verifies the StreamRequest.
func (req StreamRequest) Verify(lastTimestamp int64) error {
	// Check fields.
	if req.SrcAddr.PK.Null() {
		return ErrReqInvalidSrcPK
	}
	if req.SrcAddr.Port == 0 {
		return ErrReqInvalidSrcPort
	}
	if req.DstAddr.PK.Null() {
		return ErrReqInvalidDstPK
	}
	if req.DstAddr.Port == 0 {
		return ErrReqInvalidDstPort
	}
	if req.Timestamp <= lastTimestamp {
		return ErrReqInvalidTimestamp
	}

	// Check signature.
	if err := cipher.VerifyPubKeySignedPayload(req.SrcAddr.PK, req.raw.Sig(), req.raw.Object()); err != nil {
		return ErrReqInvalidSig.Wrap(err)
	}

	return nil
}

// StreamResponse is the response of a StreamRequest.
type StreamResponse struct {
	ReqHash  cipher.SHA256 // Hash of associated dial request.
	Accepted bool          // Whether the request is accepted.
	IP       net.IP        // IP address of the node.
	ErrCode  errorCode     // Check if not accepted.
	NoiseMsg []byte

	raw SignedObject `enc:"-"` // back reference.
}

// Verify verifies the StreamResponse.
func (resp StreamResponse) Verify(req StreamRequest) error {
	// Check fields.
	if resp.ReqHash != req.raw.Hash() {
		return ErrDialRespInvalidHash
	}

	// Check signature.
	if err := cipher.VerifyPubKeySignedPayload(req.DstAddr.PK, resp.raw.Sig(), resp.raw.Object()); err != nil {
		return ErrDialRespInvalidSig.Wrap(err)
	}

	// Check whether response states that the request is accepted.
	if !resp.Accepted {
		ok, err := ErrorFromCode(resp.ErrCode)
		if !ok {
			err = ErrDialRespNotAccepted
		}
		return err
	}

	return nil
}

// SignBytes signs the provided bytes with the given secret key.
func SignBytes(b []byte, sk cipher.SecKey) (cipher.Sig, error) {
	sig, err := cipher.SignPayload(b, sk)
	if err != nil {
		return cipher.Sig{}, fmt.Errorf("dmsg: error during sign: %w", err)
	}
	return sig, nil
}
