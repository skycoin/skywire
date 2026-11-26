// Package dmsg pkg/dmsg/dmsg/ipproof.go c1-net-dmsg
package dmsg

import (
	"errors"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

var (
	// ErrIPProofNotIPRequest is returned for a proof whose request did not ask for an IP.
	ErrIPProofNotIPRequest = errors.New("ip proof: request is not an IP request")
	// ErrIPProofNoIP is returned for a proof whose response carries no IP.
	ErrIPProofNoIP = errors.New("ip proof: response has no IP")
	// ErrIPProofUndated is returned for a proof from a server too old to date it.
	ErrIPProofUndated = errors.New("ip proof: response has no server timestamp")
)

// IPProof is a client's signed IP request kept with the server's signed
// answer, so a third party can check the IP the server saw for that client.
type IPProof struct {
	Request  SignedObject `json:"request"`
	Response SignedObject `json:"response"`
}

// IPProofClaim is what a verified IPProof shows.
type IPProofClaim struct {
	IP     net.IP
	Client cipher.PubKey
	Server cipher.PubKey
	Time   time.Time
}

// Verify checks both signatures, that the response answers the request, and
// that the server dated it. Whether the server is trusted is the caller's call.
func (p IPProof) Verify() (IPProofClaim, error) {
	req, err := p.Request.ObtainStreamRequest()
	if err != nil {
		return IPProofClaim{}, err
	}
	if !req.IPinfo {
		return IPProofClaim{}, ErrIPProofNotIPRequest
	}
	if err := req.Verify(0); err != nil {
		return IPProofClaim{}, err
	}
	resp, err := p.Response.ObtainStreamResponse()
	if err != nil {
		return IPProofClaim{}, err
	}
	if err := resp.Verify(req); err != nil {
		return IPProofClaim{}, err
	}
	if len(resp.IP) == 0 {
		return IPProofClaim{}, ErrIPProofNoIP
	}
	if resp.Timestamp == 0 {
		return IPProofClaim{}, ErrIPProofUndated
	}
	return IPProofClaim{
		IP:     resp.IP,
		Client: req.SrcAddr.PK,
		Server: req.DstAddr.PK,
		Time:   time.Unix(0, resp.Timestamp),
	}, nil
}
