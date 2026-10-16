//go:build linux && !android

// Package vpn pkg/vpn/skydns_tun_linux.go c4-app-vpn
package vpn

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/songgao/water"

	"github.com/skycoin/skywire/pkg/skydns"
	"github.com/skycoin/skywire/pkg/util/osutil"
	"github.com/skycoin/skywire/pkg/vpn/netctl"
)

// SkyDNSTunName is the persistent tun SkyDNS uses when it runs without
// CAP_NET_ADMIN. It is set up once as root and then opened by its owner.
const SkyDNSTunName = "skydns0"

// SetupSkyDNSTun creates SkyDNS's persistent tun owned by uid:gid, routes
// skydns.Range into it and sends the host's mesh zones to it. It runs as root,
// behind the one password prompt an unprivileged SkyDNS asks for.
func SetupSkyDNSTun(uid, gid int) error {
	if _, err := os.Stat(resolvedSocket); err != nil {
		return fmt.Errorf("skydns without CAP_NET_ADMIN needs systemd-resolved: %w", err)
	}
	if _, err := net.InterfaceByName(SkyDNSTunName); err != nil {
		t, err := water.New(water.Config{
			DeviceType: water.TUN,
			PlatformSpecificParams: water.PlatformSpecificParams{
				Name:        SkyDNSTunName,
				Persist:     true,
				Permissions: &water.DevicePermissions{Owner: uint(uid), Group: uint(gid)}, //nolint:gosec
			},
		})
		if err != nil {
			return fmt.Errorf("create %s: %w", SkyDNSTunName, err)
		}
		_ = t.Close() //nolint:errcheck
	}
	for _, step := range []func() error{
		func() error { return netctl.AddrAdd(SkyDNSTunName, skydns.LocalAddr+"/32") },
		func() error { return netctl.SetMTU(SkyDNSTunName, TUNMTU) },
		func() error { return netctl.LinkUp(SkyDNSTunName) },
		func() error { return netctl.RouteReplaceDev(skydns.Range, SkyDNSTunName) },
	} {
		if err := step(); err != nil && !strings.Contains(err.Error(), "exists") {
			return err
		}
	}
	_, err := resolvedSplitDNS(SkyDNSTunName, "")
	return err
}

// ensureSkyDNSTun sets up the persistent tun when it is missing, asking for a
// password once through pkexec, the way the VPN apps escalate.
func ensureSkyDNSTun() error {
	if skyDNSTunReady() {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	owner := strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
	return osutil.RunElevated(exe, "app", "skydns", "--setup-tun", owner)
}

// skyDNSTunReady reports whether the persistent tun exists, is up and routes
// skydns.Range, so a later start needs no prompt.
func skyDNSTunReady() bool {
	ifc, err := net.InterfaceByName(SkyDNSTunName)
	if err != nil || ifc.Flags&net.FlagUp == 0 {
		return false
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if strings.HasPrefix(a.String(), skydns.LocalAddr+"/") {
			return skyDNSZonesSet()
		}
	}
	return false
}

// openSkyDNSTun attaches to the persistent tun as its owner, which needs no
// privilege.
func openSkyDNSTun() (TUNDevice, error) {
	t, err := water.New(water.Config{
		DeviceType:             water.TUN,
		PlatformSpecificParams: water.PlatformSpecificParams{Name: SkyDNSTunName, Persist: true},
	})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", SkyDNSTunName, err)
	}
	return t, nil
}

// skyDNSZonesSet reports whether systemd-resolved still sends the mesh zones
// to the tun. A resolved restart forgets them while the tun stays up.
func skyDNSZonesSet() bool {
	out, err := osutil.RunWithResult("resolvectl", "domain", SkyDNSTunName)
	return err == nil && strings.Contains(string(out), "~dmsg") && strings.Contains(string(out), "~skynet")
}
