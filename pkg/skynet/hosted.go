// Package skynet pkg/skynet/hosted.go
package skynet

import (
	"github.com/skycoin/skywire/pkg/cipher"
)

// hostedPayload is what a service key signs to name the visor hosting it.
func hostedPayload(service, host cipher.PubKey) []byte {
	const tag = "skywire hosted service v1"
	b := make([]byte, 0, len(tag)+2*len(service))
	b = append(b, tag...)
	b = append(b, service[:]...)
	return append(b, host[:]...)
}

// SignHosted signs, with the service's key, that host runs the service. A
// host returns it when it hands a stream to the service, so a caller knows
// the visor it reached may speak for that key.
func SignHosted(service cipher.PubKey, serviceSK cipher.SecKey, host cipher.PubKey) (cipher.Sig, error) {
	return cipher.SignPayload(hostedPayload(service, host), serviceSK)
}

// VerifyHosted checks a SignHosted signature.
func VerifyHosted(service, host cipher.PubKey, sig cipher.Sig) error {
	return cipher.VerifyPubKeySignedPayload(service, sig, hostedPayload(service, host))
}
