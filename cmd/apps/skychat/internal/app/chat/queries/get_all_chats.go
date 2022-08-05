package queries

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/peer"
)

// GetAllChatsResult is the result of the GetAllChatsRequest Query
type GetAllChatsResult struct {
	Pk    cipher.PubKey
	CType int
	Info  info.Info
	Msgs  []message.Message
	Peers []peer.Peer
}

//GetAllChatsRequestHandler Contains the dependencies of the Handler
type GetAllChatsRequestHandler interface {
	Handle() ([]GetAllChatsResult, error)
}

type getAllChatsRequestHandler struct {
	repo chat.Repository
}

//NewGetAllChatsRequestHandler Handler constructor
func NewGetAllChatsRequestHandler(repo chat.Repository) GetAllChatsRequestHandler {
	return getAllChatsRequestHandler{repo: repo}
}

//Handle Handles the query
func (h getAllChatsRequestHandler) Handle() ([]GetAllChatsResult, error) {

	res, err := h.repo.GetAll()
	if err != nil {
		return nil, err
	}
	var result []GetAllChatsResult
	for _, chat := range res {
		result = append(result, GetAllChatsResult{Pk: chat.GetPK(), CType: chat.GetType(), Info: chat.GetInfo(), Msgs: chat.GetMessages(), Peers: chat.GetPeers()})
	}
	return result, nil
}
