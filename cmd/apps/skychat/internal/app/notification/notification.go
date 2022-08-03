package notification

import (
	"encoding/json"
	"fmt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/message"
)

const (
	// ErrNotifType notify about errors
	ErrNotifType        = iota
	NewAddChatNotifType //notify about an added chat by the user
	NewChatNotifType    //notify about a new chat initiated by a peer
	NewMsgNotifType     //notify about new message
	//DeleteChatNotifType //notify about a deleted chat
	//TODO: add SentMsgnNotifType
)

// Notification provides a struct to send messages via the Service
type Notification struct {
	Type    int64  `json:"type"`
	Message string `json:"message"`
}

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
		Type:    NewMsgNotifType,
		Message: string(clientMsg),
	}
}

func NewAddChatNotification(pk cipher.PubKey) Notification {
	clientMsg, err := json.Marshal(map[string]string{"pk": pk.Hex()})
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
	}
	return Notification{
		Type:    NewAddChatNotifType,
		Message: string(clientMsg),
	}
}

func NewChatNotification(pk cipher.PubKey) Notification {
	clientMsg, err := json.Marshal(map[string]string{"pk": pk.Hex()})
	if err != nil {
		fmt.Printf("Failed to marshal json: %v", err)
	}
	return Notification{
		Type:    NewChatNotifType,
		Message: string(clientMsg),
	}
}

// Service sends Notification
type Service interface {
	GetChannel() chan string
	Notify(notification Notification) error
}
