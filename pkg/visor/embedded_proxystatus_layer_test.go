package visor

import (
	"context"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/proxystatus"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// testPK is a deterministic, FULL public key for alias assertions.
func testPK(t *testing.T) cipher.PubKey {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return pk
}

// TestStatusSnapshotDmsgLayer proves the dmsg surface's snapshot is populated
// from the embedded runtime itself — listener, suffix, chain target and the
// runtime's own Stats — and that the chain target is labeled from what the visor
// KNOWS sits behind it (the skynet_web runtime on that port) rather than probed.
func TestStatusSnapshotDmsgLayer(t *testing.T) {
	pk := testPK(t)
	log := logging.MustGetLogger("test")
	ctx := context.Background()

	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{PK: pk}}}
	v.embeddedDmsgWeb = newEmbeddedDmsgWeb(ctx, nil, nil, pk, nil, nil,
		map[string]cipher.PubKey{"tpd": pk}, nil,
		&visorconfig.DmsgWebConfig{Enable: true, UpstreamSOCKS: "127.0.0.1:4446"}, log)
	v.embeddedSkynetWeb = newEmbeddedSkynetWeb(ctx, nil, nil, &v.skynetFwdMux, pk, nil, nil,
		&visorconfig.SkynetWebConfig{Enable: true}, log)

	snap, err := v.proxyStatusProvider().StatusSnapshot(proxystatus.SurfaceDmsg)
	if err != nil {
		t.Fatalf("StatusSnapshot: %v", err)
	}
	if snap.Layer == nil {
		t.Fatal("dmsg snapshot has no layer section")
	}
	if snap.Layer.Listen != "127.0.0.1:4445" {
		t.Errorf("listen = %q, want the default dmsg_web listener", snap.Layer.Listen)
	}
	if snap.Layer.Suffix != ".dmsg" {
		t.Errorf("suffix = %q, want .dmsg", snap.Layer.Suffix)
	}
	if snap.Layer.Upstream != "127.0.0.1:4446" {
		t.Errorf("upstream = %q, want the configured chain target", snap.Layer.Upstream)
	}
	// The skynet_web runtime binds :4446 and is NOT started here, so the label
	// must say stopped — never "running", and never a fabricated reachability.
	if snap.Layer.UpstreamState != "skynet_web resolving proxy · stopped" {
		t.Errorf("upstream state = %q, want the skynet_web stopped label", snap.Layer.UpstreamState)
	}
	// Stats exist from construction, so uptime is tracked even before a request.
	if snap.Layer.StartedAt == nil {
		t.Error("layer has no started-at; Stats should clock from construction")
	}
	if snap.Layer.Requests != 0 || snap.Layer.LastRequestAt != nil {
		t.Error("an untouched layer must report zero requests and no last request")
	}

	// Names: the configured aliases, with FULL keys.
	if snap.Names == nil || len(snap.Names.Aliases) == 0 {
		t.Fatal("dmsg snapshot has no name-resolution section")
	}
	found := false
	for _, a := range snap.Names.Aliases {
		if a.Name == "tpd" {
			found = true
			if a.PK != pk.String() {
				t.Errorf("alias tpd = %q, want the full key %q", a.PK, pk.String())
			}
		}
	}
	if !found {
		t.Error("configured service alias tpd missing from the names section")
	}

	// No dmsg client wired, so no sessions may be invented.
	if len(snap.Sessions) != 0 {
		t.Errorf("sessions = %d, want none without a dmsg client", len(snap.Sessions))
	}
	// The dmsg surface owns no route plane, so it must carry no legs.
	if len(snap.Legs) != 0 || len(snap.Tunnels) != 0 {
		t.Error("dmsg surface must not carry route-group legs")
	}
}

// TestStatusSnapshotSkynetLayer proves the skynet surface's snapshot carries its
// own layer summary, the skysocks chain label, and the skynet-plane forwarded
// ports — and that non-skynet forwards are filtered out.
func TestStatusSnapshotSkynetLayer(t *testing.T) {
	pk := testPK(t)
	log := logging.MustGetLogger("test")
	ctx := context.Background()

	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{PK: pk}}, forwardedPorts: NewForwardedPorts("")}
	v.embeddedSkynetWeb = newEmbeddedSkynetWeb(ctx, nil, nil, &v.skynetFwdMux, pk, nil, nil,
		&visorconfig.SkynetWebConfig{Enable: true, UpstreamSOCKS: "127.0.0.1:1080"}, log)

	// One skynet forward and one dmsg-only forward: only the former belongs on
	// the skynet page.
	if err := v.forwardedPorts.Register(ForwardedPort{Port: 8080, LocalPort: 3000, Label: "web", Skynet: true}); err != nil {
		t.Fatalf("register skynet port: %v", err)
	}
	if err := v.forwardedPorts.Register(ForwardedPort{Port: 9090, DMSG: true}); err != nil {
		t.Fatalf("register dmsg port: %v", err)
	}

	snap, err := v.proxyStatusProvider().StatusSnapshot(proxystatus.SurfaceSkynet)
	if err != nil {
		t.Fatalf("StatusSnapshot: %v", err)
	}
	if snap.Layer == nil {
		t.Fatal("skynet snapshot has no layer section")
	}
	if snap.Layer.Listen != "127.0.0.1:4446" || snap.Layer.Suffix != ".skynet" {
		t.Errorf("listen/suffix = %q/%q, want the skynet_web defaults", snap.Layer.Listen, snap.Layer.Suffix)
	}
	// :1080 is the skysocks-client's address; with no proc manager it is stopped.
	if snap.Layer.UpstreamState != "skysocks-client · stopped" {
		t.Errorf("upstream state = %q, want the skysocks-client stopped label", snap.Layer.UpstreamState)
	}
	if len(snap.Forwards) != 1 {
		t.Fatalf("forwards = %d, want only the skynet-plane forward", len(snap.Forwards))
	}
	if snap.Forwards[0].Port != 8080 || snap.Forwards[0].LocalPort != 3000 || snap.Forwards[0].Label != "web" {
		t.Errorf("forward = %+v, want the registered skynet forward", snap.Forwards[0])
	}
	// The skynet page has no dmsg sessions and no names for a foreign plane.
	if len(snap.Sessions) != 0 {
		t.Error("skynet surface must not carry dmsg sessions")
	}
}

// TestStatusSnapshotLayerDisabled proves a visor with neither resolver
// constructed still renders an honest page: a stopped layer plus a note saying
// why, rather than a panic or a blank section.
func TestStatusSnapshotLayerDisabled(t *testing.T) {
	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{}}}
	for _, surface := range []proxystatus.Surface{proxystatus.SurfaceDmsg, proxystatus.SurfaceSkynet} {
		snap, err := v.proxyStatusProvider().StatusSnapshot(surface)
		if err != nil {
			t.Fatalf("StatusSnapshot(%s): %v", surface, err)
		}
		if snap.Layer == nil {
			t.Fatalf("%s: no layer section for a disabled resolver", surface)
		}
		if snap.Running {
			t.Errorf("%s: a non-constructed resolver must not report running", surface)
		}
		if snap.Note == "" {
			t.Errorf("%s: expected a note explaining the resolver is not enabled", surface)
		}
		if snap.Layer.UpstreamState != "" {
			t.Errorf("%s: no chain claim may be made without a runtime", surface)
		}
		// The page must still render from this snapshot.
		if len(proxystatus.Render(snap)) == 0 {
			t.Errorf("%s: empty page rendered", surface)
		}
	}
}

// TestChainTargetStateUnknown proves an address the visor cannot vouch for gets
// NO liveness label — the page shows the bare address instead of implying it
// probed something.
func TestChainTargetStateUnknown(t *testing.T) {
	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{}}}
	p := &visorStatusProvider{v: v}
	for _, addr := range []string{"", "not-an-address", "127.0.0.1:9999"} {
		if got := p.chainTargetState(addr); got != "" {
			t.Errorf("chainTargetState(%q) = %q, want no claim", addr, got)
		}
	}
}

func TestResolverListenAddr(t *testing.T) {
	cases := []struct {
		addr string
		port uint
		want string
	}{
		{"", 4445, "127.0.0.1:4445"},      // empty host means loopback
		{"0.0.0.0", 4446, "0.0.0.0:4446"}, // an explicit LAN bind is preserved
		{"", 0, ""},                       // port 0 disables the SOCKS5 front-end
	}
	for _, c := range cases {
		if got := resolverListenAddr(c.addr, c.port); got != c.want {
			t.Errorf("resolverListenAddr(%q,%d) = %q, want %q", c.addr, c.port, got, c.want)
		}
	}
}
