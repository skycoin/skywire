package settings

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// Settings defines chat setting
type Settings struct {
	blacklist []cipher.PubKey // Blacklist to block inocming connections
}

// GetBlacklist returns the blacklist
func (s Settings) GetBlacklist() []cipher.PubKey {
	return s.blacklist
}

// SetBlacklist sets the blacklist
func (s *Settings) SetBlacklist(bl []cipher.PubKey) {
	s.blacklist = bl
}

// InBlacklist checks blacklist for a public key
func (s Settings) InBlacklist(pk cipher.PubKey) bool {
	for _, b := range s.blacklist {
		if b == pk {
			return true
		}
	}
	return false
}

// NewDefaultSettings Constructor for default settings
func NewDefaultSettings() Settings {
	s := Settings{}
	s.blacklist = []cipher.PubKey{}
	return s
}

// NewSettings blacklists a public key
func NewSettings(blacklist []cipher.PubKey) Settings {
	s := Settings{}
	s.blacklist = blacklist
	return s
}

// IsEmpty checks if the blacklist is empty
func (s *Settings) IsEmpty() bool {
	return len(s.blacklist) <= 0
}
