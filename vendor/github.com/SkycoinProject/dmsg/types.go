package dmsg

import (
	"fmt"
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
)

const (
	// Type returns the stream type string.
	Type = "dmsg"

	// HandshakePayloadVersion contains payload version to maintain compatibility with future versions
	// of HandshakeData format.
	HandshakePayloadVersion = "2.0"
)

var (
	// HandshakeTimeout defines the duration a stream handshake should take.
	HandshakeTimeout = time.Second * 20

	// AcceptBufferSize defines the size of the accepts buffer.
	AcceptBufferSize = 20
)

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

/* Request & Response */

// StreamDialRequest represents a stream dial request object.
type StreamDialRequest struct {
	Timestamp int64
	SrcAddr   Addr
	DstAddr   Addr
	NoiseMsg  []byte
	Sig       cipher.Sig
}

// Empty returns true if the dial request is empty.
func (dr *StreamDialRequest) Empty() bool {
	return dr.Timestamp == 0
}

// Sign signs the dial request with the given secret key.
func (dr *StreamDialRequest) Sign(sk cipher.SecKey) {
	dr.Sig = cipher.Sig{}
	b := encodeGob(dr)
	sig, err := cipher.SignPayload(b, sk)
	if err != nil {
		panic(err)
	}
	dr.Sig = sig
}

// Hash returns the hash of the dial request object.
func (dr StreamDialRequest) Hash() cipher.SHA256 {
	dr.Sig = cipher.Sig{}
	return cipher.SumSHA256(encodeGob(dr))
}

// Verify verifies the dial request object.
func (dr StreamDialRequest) Verify(lastTimestamp int64) error {
	if dr.SrcAddr.PK.Null() {
		return ErrReqInvalidSrcPK
	}
	if dr.SrcAddr.Port == 0 {
		return ErrReqInvalidSrcPort
	}
	if dr.DstAddr.PK.Null() {
		return ErrReqInvalidDstPK
	}
	if dr.DstAddr.Port == 0 {
		return ErrReqInvalidDstPort
	}
	if dr.Timestamp <= lastTimestamp {
		return ErrReqInvalidTimestamp
	}

	sig := dr.Sig
	dr.Sig = cipher.Sig{}

	if err := cipher.VerifyPubKeySignedPayload(dr.SrcAddr.PK, sig, encodeGob(dr)); err != nil {
		return ErrReqInvalidSig
	}
	return nil
}

// StreamDialResponse is the response of a StreamDialRequest.
type StreamDialResponse struct {
	ReqHash  cipher.SHA256 // Hash of associated dial request.
	Accepted bool          // Whether the request is accepted.
	ErrCode  uint16        // Check if not accepted.
	NoiseMsg []byte
	Sig      cipher.Sig // Signature of this DialRequest, signed with public key of receiving node.
}

// Sign signs the dial response.
func (dr *StreamDialResponse) Sign(sk cipher.SecKey) {
	dr.Sig = cipher.Sig{}
	b := encodeGob(dr)
	sig, err := cipher.SignPayload(b, sk)
	if err != nil {
		panic(err)
	}
	dr.Sig = sig
}

// Verify verifies the dial response.
func (dr StreamDialResponse) Verify(reqDstPK cipher.PubKey, reqHash cipher.SHA256) error {
	if dr.ReqHash != reqHash {
		return ErrDialRespInvalidHash
	}

	sig := dr.Sig
	dr.Sig = cipher.Sig{}

	if err := cipher.VerifyPubKeySignedPayload(reqDstPK, sig, encodeGob(dr)); err != nil {
		return ErrDialRespInvalidSig
	}
	if !dr.Accepted {
		ok, err := ErrorFromCode(dr.ErrCode)
		if !ok {
			return ErrDialRespNotAccepted
		}
		return err
	}
	return nil
}
