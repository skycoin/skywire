// Package peer contains the code required by the chat app for peering
package peer

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
)

// Peer contains information about a peer
type Peer struct {
	// Info is the info that is managed and updated by the peer
	Info info.Info
	// Alias is a custom alias that can be set by the user
	Alias string
}

// GetPK returns the public key of the peer
func (p *Peer) GetPK() cipher.PubKey {
	return p.Info.GetPK()
}

// GetInfo returns the info of the peer
func (p *Peer) GetInfo() info.Info {
	return p.Info
}

// GetAlias returns the alias of the peer
func (p *Peer) GetAlias() string {
	return p.Alias
}

// SetInfo updates the info of the peer with the given info
func (p *Peer) SetInfo(i info.Info) {
	p.Info = i
}

// SetAlias sets or updates the peer with the given alias
func (p *Peer) SetAlias(a string) {
	p.Alias = a
}

// NewPeer is constructor for Peer
func NewPeer(i info.Info, alias string) *Peer {
	if alias == "" {
		return &Peer{i, i.GetAlias()}
	}
	return &Peer{i, alias}
}
