// Package chat contains the interface Repository for domain
package chat

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// Repository is the interface to the chat repository
type Repository interface {
	GetByPK(pk cipher.PubKey) (*Visor, error)
	GetAll() ([]Visor, error)
	Add(v Visor) error
	Set(v Visor) error
	Delete(pk cipher.PubKey) error

	Close() error
}
