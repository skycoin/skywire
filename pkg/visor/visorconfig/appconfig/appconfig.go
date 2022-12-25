// Package appconfig pkg/visor/visorconfig/appconfig/appconfig.go
package appconfig


import (
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	)

// AppConfig defines app startup parameters.
type AppConfig struct {
	Name      string       `json:"name"`
	Args      []string     `json:"args,omitempty"`
	AutoStart bool         `json:"auto_start"`
	Port      uint16 `json:"port"`
}

// AppStatus defines running status of an App.
type AppStatus int

// AppState defines state parameters for a registered App.
type AppState struct {
	AppConfig
	Status         AppStatus `json:"status"`
	DetailedStatus string    `json:"detailed_status"`
}

// AppLauncherConfig configures the launcher.
type AppLauncherConfig struct {
	VisorPK       cipher.PubKey
	Apps          []AppConfig
	ServerAddr    string
	BinPath       string
	LocalPath     string
	DisplayNodeIP bool
}

// AppLauncher is responsible for launching and keeping track of app states.
type AppLauncher struct {
	conf  AppLauncherConfig
	log   logrus.FieldLogger
	r     router.Router
	procM ProcManager
	apps  map[string]AppConfig
	mx    sync.Mutex
}
