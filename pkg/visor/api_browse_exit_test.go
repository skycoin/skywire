package visor

import (
	"testing"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// TestBrowseDefaultExit pins where an unnamed clearnet browse gets its exit:
// the skysocks-client's --srv. It also pins the app-name match itself —
// GetSkysocksClientAddress compared the app's name against the client's
// LISTEN ADDRESS constant (":1080") and so never matched anything, which left
// every caller believing no skysocks server was configured.
func TestBrowseDefaultExit(t *testing.T) {
	exit, _ := cipher.GenerateKeyPair()
	v := &Visor{conf: &visorconfig.V1{Launcher: &visorconfig.Launcher{}}}
	if _, ok := v.browseDefaultExit(); ok {
		t.Fatal("no apps configured: must report no exit")
	}
	v.conf.Launcher.Apps = []appserver.AppConfig{{
		Name: skyenv.SkysocksClientName,
		Args: []string{"app", "skysocks-client", "--srv", exit.Hex(), "--addr", skyenv.SkysocksClientAddr},
	}}
	got, ok := v.browseDefaultExit()
	if !ok || got != exit {
		t.Fatalf("browseDefaultExit = (%s,%v), want (%s,true)", got, ok, exit)
	}
	if v.GetSkysocksClientAddress() != exit.Hex() {
		t.Fatal("GetSkysocksClientAddress must find the skysocks-client by its app name")
	}
}
