// Package group pkg/skychat/group/store_key_test.go c4-app-chat
package group

import "github.com/skycoin/skywire/pkg/cipher"

// testStoreSK is the store-sealing key every test store opens with. It needs
// to be a real key, since the sealer refuses the zero value.
func testStoreSK() cipher.SecKey {
	_, sk := cipher.GenerateKeyPair()
	return sk
}
