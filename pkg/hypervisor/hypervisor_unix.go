//+build !windows

package hypervisor

import (
	"net/http"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/dmsgpty"
	"github.com/SkycoinProject/skywire-mainnet/pkg/skyenv"
)

type dmsgPtyUI struct {
	PtyUI *dmsgpty.UI
}

func (vc *VisorConn) setupDmsgPtyUI(dmsgC *dmsg.Client, visorPK cipher.PubKey) {
	ptyDialer := dmsgpty.DmsgUIDialer(dmsgC, dmsg.Addr{PK: visorPK, Port: skyenv.DmsgPtyPort})
	vc.PtyUI = &dmsgPtyUI{
		PtyUI: dmsgpty.NewUI(ptyDialer, dmsgpty.DefaultUIConfig()),
	}
}

func (hv *Hypervisor) getPty() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		ctx.PtyUI.PtyUI.Handler()(w, r)
	})
}
