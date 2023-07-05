// Package user contains the code required by user of the chat app
package user

import (
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/peer"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/settings"
)

// User defines the user
type User struct {
	Info     info.Info         // the info of the local user
	Settings settings.Settings // the settings of the local user
	Peerbook peer.Peerbook     // the contactsbook of the local user
}

// GetInfo gets the user info
func (u *User) GetInfo() *info.Info {
	return &u.Info
}

// GetSettings returns *settings.Settings
func (u *User) GetSettings() *settings.Settings {
	return &u.Settings
}

// GetPeerbook returns the peerbook
func (u *User) GetPeerbook() *peer.Peerbook {
	return &u.Peerbook
}

// SetInfo sets the user's info
func (u *User) SetInfo(i info.Info) {
	u.Info = i
}

// SetSettings applies settings
func (u *User) SetSettings(s settings.Settings) {
	u.Settings = s
}

// SetPeerbook sets the peerbook
func (u *User) SetPeerbook(p peer.Peerbook) {
	u.Peerbook = p
}

// NewDefaultUser returns *User
func NewDefaultUser() *User {
	u := User{}

	u.Info = info.NewDefaultInfo()
	u.Settings = settings.NewDefaultSettings()

	return &u
}

// IsEmpty checks if the user is empty
func (u *User) IsEmpty() bool {
	if u.Info.IsEmpty() && u.Settings.IsEmpty() {
		return true
	}
	return false
}
