package visor

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgc"
)

// The folded server's own wss suffix wins and needs no deployment listing. A key
// the deployment does not list never gets the deployment's suffix.
func TestFoldedWSSSuffix(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	if s, dep := foldedWSSSuffix(&dmsgc.DmsgServerConfig{WSSDomainSuffix: ".dmsg.example.net"}, pk); s != "dmsg.example.net" || dep {
		t.Errorf("own suffix: %q from deployment %v", s, dep)
	}
	if s, dep := foldedWSSSuffix(&dmsgc.DmsgServerConfig{}, pk); s != "" || dep {
		t.Errorf("unlisted key, no own suffix: %q from deployment %v", s, dep)
	}
}
