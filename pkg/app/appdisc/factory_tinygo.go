//go:build tinygo

// Package appdisc pkg/app/appdisc/factory_tinygo.go c2-vis-appsvc
//
// TinyGo (browser/wasm) Factory updater constructors. Service discovery is an
// HTTP (servicedisc → net/http) concern that does not compile here and that a
// browser visor does not perform, so every updater is the no-op emptyUpdater.
// PublicVisorUpdater is intentionally absent — it is native-only (public-visor
// validation is not a browser-leaf concern, and the type lives in the native
// build); no TinyGo-compiled caller references it.
package appdisc

import "github.com/skycoin/skywire/pkg/app/appcommon"

// VisorUpdater returns a no-op updater on TinyGo builds.
func (f *Factory) VisorUpdater(_ uint16) Updater {
	f.setDefaults()
	return &emptyUpdater{}
}

// AppUpdater returns a no-op updater on TinyGo builds.
func (f *Factory) AppUpdater(_ appcommon.ProcConfig) (Updater, bool) {
	f.setDefaults()
	return &emptyUpdater{}, false
}
