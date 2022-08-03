package queries

import (
	"net"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/peer"
)

//GetChatByPK Model of the Handler
type GetChatByPKRequest struct {
	Pk cipher.PubKey
}

// GetChatByPKResult is the result of the GetChatByPKRequest Query
type GetChatByPKResult struct {
	Pk    cipher.PubKey
	CType int
	Conn  net.Conn
	Info  info.Info
	Msgs  []message.Message
	Peers []peer.Peer
}

//GetChatByPKRequestHandler Contains the dependencies of the Handler
type GetChatByPKRequestHandler interface {
	Handle(query GetChatByPKRequest) (GetChatByPKResult, error)
}

type getChatByPKRequestHandler struct {
	repo chat.ChatRepository
}

//NewGetChatByPKRequestHandler Handler constructor
func NewGetChatByPKRequestHandler(repo chat.ChatRepository) GetChatByPKRequestHandler {
	return getChatByPKRequestHandler{repo: repo}
}

//Handle Handles the query
func (h getChatByPKRequestHandler) Handle(query GetChatByPKRequest) (GetChatByPKResult, error) {

	res, err := h.repo.GetByPK(query.Pk)
	var result GetChatByPKResult

	if err != nil {
		return result, err
	}
	result = GetChatByPKResult{Pk: res.GetPK(), CType: res.GetType(), Conn: res.GetConnection(), Info: res.GetInfo(), Msgs: res.GetMessages(), Peers: res.GetPeers()}

	return result, nil
}
