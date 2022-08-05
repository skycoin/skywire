package peer

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
)

// Peer contains information about a peer
type Peer struct {
	Info info.Info
	//TODO: make peerRepository so the user can give each Peer a Custom Alias
	//TODO: CustomAlias string
}
