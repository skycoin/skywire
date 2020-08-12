//+build windows

package hypervisor

import (
	"net/http"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
)

// dmsgPtyUI is a dummy to exclude `*dmsgpty.UI` source file from building for Windows.
type dmsgPtyUI struct {
}

func setupDmsgPtyUI(dmsgC *dmsg.Client, visorPK cipher.PubKey) *dmsgPtyUI {
	// this method doesn't depend on config values and will be invoked anyway,
	// so this dummy is needed
	return nil
}

func (hv *Hypervisor) getPty() http.HandlerFunc {
	// this one won't be invoked, but to exclude some non-building source files for Windows,
	// it's needed
	return nil
}
