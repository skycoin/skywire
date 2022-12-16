// Package user contains the code required by user of the chat app
package user

import (
	"fmt"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/peer"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/settings"
)

// User defines the user
type User struct {
	info     info.Info         // the info of the local user
	settings settings.Settings // the settings of the local user
	peerbook peer.Peerbook     // the contactsbook of the local user
}

// GetInfo gets the user info
func (u *User) GetInfo() *info.Info {
	return &u.info
}

// GetSettings returns *settings.Settings
func (u *User) GetSettings() *settings.Settings {
	return &u.settings
}

// GetPeerbook returns the peerbook
func (u *User) GetPeerbook() *peer.Peerbook {
	return &u.peerbook
}

// SetInfo sets the user's info
func (u *User) SetInfo(i info.Info) {
	u.info = i
}

// SetSettings applies settings
func (u *User) SetSettings(s settings.Settings) {
	u.settings = s
}

// SetPeerbook sets the peerbook
func (u *User) SetPeerbook(p peer.Peerbook) {
	u.peerbook = p
}

// NewDefaultUser returns *User
func NewDefaultUser() *User {
	fmt.Println("user - NewDefaultUser")
	u := User{}

	u.info = info.NewDefaultInfo()
	u.settings = settings.NewDefaultSettings()

	return &u
}

// IsEmpty checks if the user is empty
func (u *User) IsEmpty() bool {
	if u.info.IsEmpty() && u.settings.IsEmpty() {
		return true
	}
	return false
}
