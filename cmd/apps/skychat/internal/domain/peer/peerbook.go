package peer

import (
	"fmt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
)

// Peerbook contains a map of peers
type Peerbook struct {
	peers map[cipher.PubKey]Peer
}

// GetPeerByPK returns a peer from the peerbook if it is available
func (pb *Peerbook) GetPeerByPK(pk cipher.PubKey) (*Peer, error) {
	if p, ok := pb.peers[pk]; ok {
		return &p, nil
	}
	return nil, fmt.Errorf("peer not found")
}

// SetPeer updates or adds the given peer in the peerbook
func (pb *Peerbook) SetPeer(p Peer) {
	pb.peers[p.GetPK()] = p
}

// DeletePeer deletes the given peer from the peerbook
func (pb *Peerbook) DeletePeer(pk cipher.PubKey) {
	delete(pb.peers, pk)
}

// AddPeer adds a new peer to the peerbook
func (pb *Peerbook) AddPeer(i info.Info, alias string) {
	p := NewPeer(i, alias)
	pb.peers[i.GetPK()] = *p
}