//go:build !tinygo

// Package appdisc pkg/app/appdisc/factory_native.go
//
// The Factory's updater constructors. They build servicedisc.HTTPClient
// instances (net/http), so they are native-only; factory_tinygo.go provides
// the TinyGo stubs that return emptyUpdater. Factory.Client is an *http.Client
// recovered from the `any`-typed field here.
package appdisc

import (
	"net/http"
	"time"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/geo"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// httpClient recovers the *http.Client stored in the `any`-typed Factory.Client
// (nil if unset — servicedisc.NewClient tolerates a nil client).
func (f *Factory) httpClient() *http.Client {
	c, _ := f.Client.(*http.Client)
	return c
}

// geoData recovers the *geo.LocationData stored in the `any`-typed Factory.Geo.
func (f *Factory) geoData() *geo.LocationData {
	g, _ := f.Geo.(*geo.LocationData)
	return g
}

// VisorUpdater obtains a visor updater.
func (f *Factory) VisorUpdater(port uint16) Updater {
	// Always return empty updater if keys are not set.
	if f.setDefaults(); f.PK.Null() || f.SK.Null() {
		return &emptyUpdater{}
	}

	conf := servicedisc.Config{
		Type:          servicedisc.ServiceTypeVisor,
		PK:            f.PK,
		SK:            f.SK,
		Port:          port,
		DiscAddr:      f.ServiceDisc,
		DisplayNodeIP: f.DisplayNodeIP,
		Geo:           f.geoData(),
	}

	return newServiceUpdater(
		f.Log,
		servicedisc.NewClient(f.Log, f.MLog, conf, f.httpClient(), f.ClientPublicIP),
		f.HeartbeatInterval,
	)
}

// PublicVisorUpdater obtains a public visor updater with validation logic.
// It wraps a regular service updater and monitors for external STCPR connections
// and transport count to determine if the visor should stay registered.
func (f *Factory) PublicVisorUpdater(
	port uint16,
	registrationTimeout time.Duration,
	maxTransports int,
	getTransportCount func() int,
) *PublicVisorUpdater {
	// Always return nil if keys are not set.
	if f.setDefaults(); f.PK.Null() || f.SK.Null() {
		return nil
	}

	conf := servicedisc.Config{
		Type:          servicedisc.ServiceTypeVisor,
		PK:            f.PK,
		SK:            f.SK,
		Port:          port,
		DiscAddr:      f.ServiceDisc,
		DisplayNodeIP: f.DisplayNodeIP,
		Geo:           f.geoData(),
	}

	inner := newServiceUpdater(
		f.Log,
		servicedisc.NewClient(f.Log, f.MLog, conf, f.httpClient(), f.ClientPublicIP),
		f.HeartbeatInterval,
	)

	return NewPublicVisorUpdater(
		f.Log,
		inner,
		registrationTimeout,
		maxTransports,
		getTransportCount,
	)
}

// AppUpdater obtains an app updater based on the app name and configuration.
func (f *Factory) AppUpdater(conf appcommon.ProcConfig) (Updater, bool) {
	// Always return empty updater if keys are not set.
	if f.setDefaults(); f.PK.Null() || f.SK.Null() {
		return &emptyUpdater{}, false
	}

	log := f.Log.WithField("appName", conf.AppName)

	// Do not update in service discovery if passcode-protected or whitelist-protected.
	if conf.ContainsFlag("passcode") && conf.ArgVal("passcode") != "" {
		return &emptyUpdater{}, false
	}
	if conf.ContainsFlag("whitelist") && conf.ArgVal("whitelist") != "" {
		return &emptyUpdater{}, false
	}

	getServiceDiscConf := func(conf appcommon.ProcConfig, sType string) servicedisc.Config {
		return servicedisc.Config{
			Type:     sType,
			PK:       f.PK,
			SK:       f.SK,
			Port:     uint16(conf.RoutingPort),
			DiscAddr: f.ServiceDisc,
			Geo:      f.geoData(),
		}
	}

	switch conf.AppName {
	case skyenv.VPNServerName:
		return newServiceUpdater(
			log,
			servicedisc.NewClient(log, f.MLog, getServiceDiscConf(conf, servicedisc.ServiceTypeVPN), f.httpClient(), f.ClientPublicIP),
			f.HeartbeatInterval,
		), true
	case skyenv.SkysocksName:
		return newServiceUpdater(
			log,
			servicedisc.NewClient(log, f.MLog, getServiceDiscConf(conf, servicedisc.ServiceTypeSkysocks), f.httpClient(), f.ClientPublicIP),
			f.HeartbeatInterval,
		), true
	default:
		return &emptyUpdater{}, false
	}
}
