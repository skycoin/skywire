// Package climdisc is the output shape of the `skywire cli mdisc` commands,
// which read the dmsg discovery.
package climdisc

import "github.com/skycoin/skywire/pkg/cipher"

// Server is one dmsg server as the discovery reports it.
type Server struct {
	PublicKey cipher.PubKey `json:"public_key"`
	// Connected is inferred, not reported: the discovery gives available
	// sessions, and this is the default maximum minus that. Worth knowing
	// before treating it as a measurement.
	Connected         int    `json:"connected"`
	AvailableSessions int    `json:"avail_sess"`
	Address           string `json:"address"`
	Version           string `json:"version"`
	Registered        int64  `json:"registered"`
}
