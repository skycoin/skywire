//go:build !mobile

// Package visor pkg/visor/hosted_service_embedded.go
//
// Recording the own-key services this visor embeds as hosted here. The mobile
// build embeds no services (embedded_services_mobile.go), so it has no
// callers there.
package visor

import (
	"encoding/json"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skynet"
)

// hostService records an own-key service as hosted here, signing with its key
// that this visor is its host, and republishes the transport list so TPD
// shows the local link visors find it by.
func (v *Visor) hostService(es *embeddedService) {
	if es.ownPK.Null() {
		return
	}
	var probe struct {
		SecKey cipher.SecKey `json:"secret_key"`
	}
	if err := json.Unmarshal(es.ownRaw, &probe); err != nil || probe.SecKey.Null() {
		es.log.WithError(err).Warn("Hosted service has no secret key here; it stays reachable over dmsg only")
		return
	}
	sig, err := skynet.SignHosted(es.ownPK, probe.SecKey, v.conf.PK)
	if err != nil {
		es.log.WithError(err).Warn("Could not sign the hosting of this service; it stays reachable over dmsg only")
		return
	}
	v.hostedKeys.Store(es.ownPK, sig)
	v.republishHosted()
}

// unhostService drops a stopped service from the hosted set.
func (v *Visor) unhostService(pk cipher.PubKey) {
	if _, had := v.hostedKeys.LoadAndDelete(pk); had {
		v.republishHosted()
	}
}
