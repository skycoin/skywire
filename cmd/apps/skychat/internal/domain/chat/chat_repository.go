package chat

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

type ChatRepository interface {
	GetByPK(pk cipher.PubKey) (*Chat, error)
	GetAll() ([]Chat, error)
	Add(c Chat) error
	Update(c Chat) error
	Delete(pk cipher.PubKey) error
}
