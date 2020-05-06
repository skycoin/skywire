package hypervisor

import (
	"net/http"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
)

type dmsgPtyUI struct {
}

func (vc *VisorConn) setupDmsgPtyUI(dmsgC *dmsg.Client, visorPK cipher.PubKey) {

}

func (hv *Hypervisor) getPty() http.HandlerFunc {
	return nil
}
