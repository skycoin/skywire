package notification

import (
	"encoding/json"
	"fmt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
)

const (
	// ErrNotifyType notifies about errors
	ErrNotifyType = iota
	//NewAddChatNotifyType notifies about an added chat by the user
	NewAddChatNotifyType
	//NewChatNotifyType notifies about a new chat initiated by a peer
	NewChatNotifyType
	//NewMsgNotifyType notifies about new message
	NewMsgNotifyType
	//DeleteChatNotifyType notifies about a deleted chat
	//DeleteChatNotifyType
	//TODO: add SentMsgNotifyType
)

// Notification provides a struct to send messages via the Service
type Notification struct {
	Type    int64  `json:"type"`
	Message string `json:"message"`
}

// NewMsgNotification notifies the user of a new message
func NewMsgNotification(pk cipher.PubKey, msg message.Message) Notification {
	Msg, err := json.Marshal(message.NewJSONMessage(msg))
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
	}

	clientMsg, err := json.Marshal(map[string]string{"pk": pk.Hex(), "message": string(Msg)})
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
	}
	return Notification{
		Type:    NewMsgNotifyType,
		Message: string(clientMsg),
	}
}

// NewAddChatNotification notifies the user about add chat request
func NewAddChatNotification(pk cipher.PubKey) Notification {
	clientMsg, err := json.Marshal(map[string]string{"pk": pk.Hex()})
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
	}
	return Notification{
		Type:    NewAddChatNotifyType,
		Message: string(clientMsg),
	}
}

// NewChatNotification notifies the user about new chat
func NewChatNotification(pk cipher.PubKey) Notification {
	clientMsg, err := json.Marshal(map[string]string{"pk": pk.Hex()})
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
	GetChannel() chan string
	Notify(notification Notification) error
}
