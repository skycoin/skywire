package settings

import (
	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

type Settings struct {
	blacklist []cipher.PubKey // Blacklist to block inocming connections
}

//Getter
func (s Settings) GetBlacklist() []cipher.PubKey {
	return s.blacklist
}

//Setter
func (s *Settings) SetBlacklist(bl []cipher.PubKey) {
	s.blacklist = bl
}

//Methods
func (s Settings) InBlacklist(pk cipher.PubKey) bool {
	for _, b := range s.blacklist {
		if b == pk {
			return true
		}
	}
	return false
}

//Constructor for default settings
func NewDefaultSettings() Settings {
	s := Settings{}
	s.blacklist = []cipher.PubKey{}
	return s
}

func NewSettings(blacklist []cipher.PubKey) Settings {
	s := Settings{}
	s.blacklist = blacklist
	return s
}

func (s *Settings) IsEmpty() bool {
	return len(s.blacklist) <= 0
}
