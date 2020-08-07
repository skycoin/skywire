//+build !windows

package hypervisor

import (
	"net/http"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/dmsgpty"

	"github.com/skycoin/skywire/pkg/skyenv"
)

// dmsgPtyUI servers as a wrapper for `*dmsgpty.UI`. this way source file with
// `*dmsgpty.UI` will be included for Unix systems and excluded for Windows.
type dmsgPtyUI struct {
	PtyUI *dmsgpty.UI
}

func setupDmsgPtyUI(dmsgC *dmsg.Client, visorPK cipher.PubKey) *dmsgPtyUI {
	ptyDialer := dmsgpty.DmsgUIDialer(dmsgC, dmsg.Addr{PK: visorPK, Port: skyenv.DmsgPtyPort})
	return &dmsgPtyUI{
		PtyUI: dmsgpty.NewUI(ptyDialer, dmsgpty.DefaultUIConfig()),
	}
}

func (hv *Hypervisor) getPty() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		ctx.PtyUI.PtyUI.Handler()(w, r)
	})
}
