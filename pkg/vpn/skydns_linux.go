//go:build linux && !android

// Package vpn pkg/vpn/skydns_linux.go c4-app-vpn
package vpn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/skydns"
	"github.com/skycoin/skywire/pkg/util/osutil"
	"github.com/skycoin/skywire/pkg/vpn/netctl"
)

const (
	resolvConfPath = "/etc/resolv.conf"
	resolvedSocket = "/run/systemd/resolve/io.systemd.Resolve"
	resolvBegin    = "# skydns begin"
	resolvEnd      = "# skydns end"
	// skyDNSResolvCheck paces putting the resolv.conf block back after a
	// network manager rewrites the file.
	skyDNSResolvCheck = 5 * time.Second
)

// RunSkyDNS serves SkyDNS on a tunnel of its own until ctx ends. Only
// skydns.Range is routed into it. The host asks it for .dmsg and .skynet names
// through systemd-resolved where that runs, and through resolv.conf otherwise.
func RunSkyDNS(ctx context.Context, cfg SkyDNSConfig) error {
	log := cfg.Log
	if log == nil {
		log = logrus.StandardLogger()
	}
	var (
		tun  TUNDevice
		res  *skyDNSResolver
		name string
		err  error
	)
	if _, perr := setupClientSysPrivileges(); perr != nil {
		// Without CAP_NET_ADMIN, ask for a password once to set up a tun this
		// user owns, then run on it. It outlives this run, so later starts
		// need no prompt.
		log.WithError(perr).Debug("skydns: no CAP_NET_ADMIN, using the persistent " + SkyDNSTunName)
		if err := ensureSkyDNSTun(); err != nil {
			return fmt.Errorf("skydns: no CAP_NET_ADMIN, and setting up %s as root failed: %w", SkyDNSTunName, err)
		}
		if tun, err = openSkyDNSTun(); err != nil {
			return err
		}
		defer tun.Close() //nolint:errcheck
		name = SkyDNSTunName
		upstream := cfg.Upstream
		if upstream == "" {
			upstream = shareDefaultDNS
		}
		res = &skyDNSResolver{mode: "systemd-resolved", upstream: upstream, keep: func() error { return nil }, undo: func() {}}
	} else {
		if tun, err = newTUNDevice(); err != nil {
			return err
		}
		defer tun.Close() //nolint:errcheck
		name = tun.Name()
		if err := netctl.AddrAdd(name, skydns.LocalAddr+"/32"); err != nil {
			return err
		}
		if err := netctl.SetMTU(name, TUNMTU); err != nil {
			return err
		}
		if err := netctl.LinkUp(name); err != nil {
			return err
		}
		if err := netctl.RouteReplaceDev(skydns.Range, name); err != nil {
			return err
		}
		if res, err = newSkyDNSResolver(name, cfg.Upstream); err != nil {
			return err
		}
	}
	defer res.undo()

	var d net.Dialer
	sky, err := skydns.New(skydns.Config{
		Dial:     cfg.Dial,
		Upstream: skydns.NewUpstream(res.upstream, d.DialContext, false),
		Minter:   cfg.Minter,
		MTU:      TUNMTU,
		Log:      log,
	})
	if err != nil {
		return err
	}
	defer sky.Close() //nolint:errcheck

	go func() {
		buf := make([]byte, TUNMTU+4)
		for {
			n, err := sky.Read(buf)
			if errors.Is(err, io.EOF) {
				return
			}
			if err == nil {
				_, _ = tun.Write(buf[:n]) //nolint:errcheck
			}
		}
	}()
	tunDone := make(chan error, 1)
	go func() {
		buf := make([]byte, TUNMTU+4)
		for {
			n, err := tun.Read(buf)
			if err != nil {
				tunDone <- err
				return
			}
			if skydns.Owns(buf[:n]) {
				_, _ = sky.Write(buf[:n]) //nolint:errcheck
			}
		}
	}()

	log.Infof("skydns: answering .dmsg and .skynet names on %s via %s, other names via %s", name, res.mode, res.upstream)
	check := time.NewTicker(skyDNSResolvCheck)
	defer check.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-tunDone:
			return err
		case <-check.C:
			if err := res.keep(); err != nil {
				log.WithError(err).Warn("skydns: could not restore resolv.conf")
			}
		}
	}
}

// skyDNSResolver points the host's lookups at SkyDNS.
type skyDNSResolver struct {
	mode     string
	upstream string
	keep     func() error
	undo     func()
}

// SkyDNSSplitDNS reports whether systemd-resolved runs here, so SkyDNS can
// answer only the mesh zones and leave every other name to the host.
func SkyDNSSplitDNS() bool {
	_, err := os.Stat(resolvedSocket)
	return err == nil
}

func newSkyDNSResolver(ifName, upstream string) (*skyDNSResolver, error) {
	if SkyDNSSplitDNS() {
		return resolvedSplitDNS(ifName, upstream)
	}
	return resolvConfDNS(upstream)
}

// resolvedSplitDNS sends only the mesh zones to SkyDNS, so the host's own
// resolver keeps every other name.
func resolvedSplitDNS(ifName, upstream string) (*skyDNSResolver, error) {
	for _, args := range [][]string{
		{"dns", ifName, skydns.ResolverAddr},
		{"domain", ifName, "~dmsg", "~skynet"},
		{"default-route", ifName, "false"},
	} {
		if err := osutil.Run("resolvectl", args...); err != nil {
			return nil, err
		}
	}
	// The tunnel is not a local network, so no LLMNR or mDNS on it. Best effort:
	// an old systemd without these settings still gets the split DNS.
	for _, proto := range []string{"llmnr", "mdns"} {
		_ = osutil.Run("resolvectl", proto, ifName, "no") //nolint:errcheck
	}
	if upstream == "" {
		upstream = shareDefaultDNS
	}
	return &skyDNSResolver{
		mode:     "systemd-resolved",
		upstream: upstream,
		keep:     func() error { return nil },
		undo:     func() { _ = osutil.Run("resolvectl", "revert", ifName) }, //nolint:errcheck
	}, nil
}

// resolvConfDNS puts SkyDNS first in resolv.conf and asks the first original
// nameserver for every other name. The block is marked so a later start
// removes one left behind by a crash.
func resolvConfDNS(upstream string) (*skyDNSResolver, error) {
	raw, err := os.ReadFile(resolvConfPath)
	if err != nil {
		return nil, err
	}
	orig := stripSkyDNSBlock(string(raw))
	if upstream == "" {
		upstream = firstNameserver(orig)
	}
	if upstream == "" {
		upstream = shareDefaultDNS
	}
	keep := func() error {
		cur, err := os.ReadFile(resolvConfPath)
		if err != nil {
			return err
		}
		if strings.HasPrefix(string(cur), resolvBegin+"\n") {
			return nil
		}
		return os.WriteFile(resolvConfPath, []byte(withSkyDNSBlock(stripSkyDNSBlock(string(cur)))), 0o644) //nolint:gosec
	}
	if err := keep(); err != nil {
		return nil, err
	}
	return &skyDNSResolver{
		mode:     resolvConfPath,
		upstream: upstream,
		keep:     keep,
		undo: func() {
			if cur, err := os.ReadFile(resolvConfPath); err == nil {
				_ = os.WriteFile(resolvConfPath, []byte(stripSkyDNSBlock(string(cur))), 0o644) //nolint:errcheck,gosec
			}
		},
	}, nil
}

// withSkyDNSBlock puts SkyDNS ahead of the file's nameservers. A short timeout
// lets lookups fall through to them quickly if SkyDNS has gone.
func withSkyDNSBlock(conf string) string {
	return resolvBegin + "\nnameserver " + skydns.ResolverAddr + "\noptions timeout:1\n" + resolvEnd + "\n" + conf
}

func stripSkyDNSBlock(conf string) string {
	var out []string
	in := false
	for _, l := range strings.SplitAfter(conf, "\n") {
		switch strings.TrimSpace(l) {
		case resolvBegin:
			in = true
			continue
		case resolvEnd:
			in = false
			continue
		}
		if !in {
			out = append(out, l)
		}
	}
	return strings.Join(out, "")
}

func firstNameserver(conf string) string {
	for _, l := range strings.Split(conf, "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == "nameserver" && net.ParseIP(f[1]) != nil {
			return f[1]
		}
	}
	return ""
}
