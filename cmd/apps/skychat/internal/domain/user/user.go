package user

import (
	"fmt"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/info"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/domain/settings"
)

type User struct {
	info     info.Info         // the info of the local user
	settings settings.Settings // the settings of the local user
}

//Getter
func (u *User) GetInfo() *info.Info {
	return &u.info
}

func (u *User) GetSettings() *settings.Settings {
	return &u.settings
}

//Setter
func (u *User) SetInfo(i info.Info) {
	u.info = i
}

func (u *User) SetSettings(s settings.Settings) {
	u.settings = s
}

//
func NewDefaultUser() *User {
	fmt.Println("user - NewDefaultUser")
	u := User{}

	u.info = info.NewDefaultInfo()
	u.settings = settings.NewDefaultSettings()
	fmt.Println(u)

	return &u
}

func (u *User) IsEmpty() bool {

	if u.info.IsEmpty() && u.settings.IsEmpty() {
		return true
	} else {
		return false
	}
}
