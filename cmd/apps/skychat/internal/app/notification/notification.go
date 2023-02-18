// Package notification contains notification related code
package notification

import (
	"encoding/json"
	"fmt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/util"
)

const (
	// ErrNotifyType notifies about errors
	ErrNotifyType = iota
	//NewAddRouteNotifyType notifies about an added route by the user
	NewAddRouteNotifyType
	//NewChatNotifyType notifies about a new chat initiated by a peer
	NewChatNotifyType
	//NewMsgNotifyType notifies about new message
	NewMsgNotifyType
	//DeleteChatNotifyType notifies about a deleted chat
	DeleteChatNotifyType
	//FUTUREFEATURE: add SentMsgNotifyType
)

// Notification provides a struct to send messages via the Service
type Notification struct {
	Type    int    `json:"type"`
	Message string `json:"message"`
}

// NewMsgNotification notifies the user of a new message
func NewMsgNotification(route util.PKRoute, msg message.Message) Notification {
	clientMsg, err := json.Marshal(map[string]string{"visorpk": route.Visor.Hex(), "serverpk": route.Server.Hex(), "roompk": route.Room.Hex()})

	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
	}
	return Notification{
		Type:    NewMsgNotifyType,
		Message: string(clientMsg),
	}
}

// NewAddRouteNotification notifies the user about added route
func NewAddRouteNotification(route util.PKRoute) Notification {
	clientMsg, err := json.Marshal(map[string]string{"visorpk": route.Visor.Hex(), "serverpk": route.Server.Hex(), "roompk": route.Room.Hex()})
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
	}
	return Notification{
		Type:    NewAddRouteNotifyType,
		Message: string(clientMsg),
	}
}

// NewP2PChatNotification notifies the user about new infos in p2p chat
func NewP2PChatNotification(pk cipher.PubKey) Notification {
	clientMsg, err := json.Marshal(map[string]string{"visorpk": pk.Hex(), "serverpk": pk.Hex(), "roompk": pk.Hex()})
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
	}
	return Notification{
		Type:    NewChatNotifyType,
		Message: string(clientMsg),
	}
}

// NewGroupChatNotification notifies the user about new chat
func NewGroupChatNotification(route util.PKRoute) Notification {
	clientMsg, err := json.Marshal(map[string]string{"visorpk": route.Visor.Hex(), "serverpk": route.Server.Hex(), "roompk": route.Room.Hex()})
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
	}
	return Notification{
		Type:    NewChatNotifyType,
		Message: string(clientMsg),
	}
}

// Service sends Notification
type Service interface {
	InitChannel()
	DeferChannel()
	GetChannel() chan string
	Notify(notification Notification) error
}
