package cliconfig

import (
	"testing"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// TestResolverCASetsBothResolvers: resolver HTTPS had no conf knob, so every
// autoconfig wrote the resolvers back without it.
func TestResolverCASetsBothResolvers(t *testing.T) {
	prevConf, prevD, prevS, prevCA := conf, enableDmsgWeb, enableSkynetWeb, resolverCA
	t.Cleanup(func() { conf, enableDmsgWeb, enableSkynetWeb, resolverCA = prevConf, prevD, prevS, prevCA })

	conf = new(visorconfig.V1)
	enableDmsgWeb, enableSkynetWeb, resolverCA = true, true, "/root/.skywire/resolver"
	configureResolvingProxies(logger)

	d, s := conf.DmsgWeb, conf.SkynetWeb
	if d == nil || !d.TLSMITM || d.TLSCAPath != "/root/.skywire/resolver/ca.crt" || d.TLSCAKeyPath != "/root/.skywire/resolver/ca.key" {
		t.Errorf("dmsg_web = %+v, want tls_mitm with the CA in /root/.skywire/resolver", d)
	}
	if s == nil || !s.TLSMITM || s.TLSCAPath != "/root/.skywire/resolver/ca.crt" || s.TLSCAKeyPath != "/root/.skywire/resolver/ca.key" {
		t.Errorf("skynet_web = %+v, want tls_mitm with the CA in /root/.skywire/resolver", s)
	}

	conf, resolverCA = new(visorconfig.V1), ""
	configureResolvingProxies(logger)
	if conf.DmsgWeb.TLSMITM || conf.SkynetWeb.TLSMITM {
		t.Error("without --resolver-ca the resolvers must stay plain")
	}
}
