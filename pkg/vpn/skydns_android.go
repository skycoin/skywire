//go:build android

// Package vpn pkg/vpn/skydns_android.go c4-app-vpn
package vpn

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/skydns"
)

const (
	// skyDNSRetry paces asking for the tunnel while it is refused or the app
	// is not listening, as while SkyVPN holds the phone's one VPN slot.
	skyDNSRetry = 2 * time.Second
	// skyDNSCheck paces asking whether the tunnel is still SkyDNS's.
	skyDNSCheck = 3 * time.Second
)

// RunSkyDNS serves SkyDNS on a tunnel of its own until ctx ends. Only
// skydns.Range is routed into it, so every other packet leaves as before.
func RunSkyDNS(ctx context.Context, cfg SkyDNSConfig) error {
	log := cfg.Log
	if log == nil {
		log = logrus.StandardLogger()
	}
	server := cfg.Upstream
	if server == "" {
		server = shareDefaultDNS
	}
	// This process is outside every tunnel, so lookups leave by the phone's own
	// network. TLS when the resolver offers it, as Android's Private DNS does.
	var d net.Dialer
	sky, err := skydns.New(skydns.Config{
		Dial:     cfg.Dial,
		Upstream: skydns.NewUpstream(server, d.DialContext, false),
		MTU:      TUNMTU,
		Log:      log,
	})
	if err != nil {
		return err
	}
	defer sky.Close() //nolint:errcheck

	tun := &androidTUN{}
	defer tun.Close() //nolint:errcheck
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

	req := androidTUNRequest{
		Op:    androidTUNOpEstablish,
		Mode:  androidTUNModeDNS,
		Addr:  skydns.LocalAddr + "/32",
		MTU:   TUNMTU,
		DNS:   skydns.ResolverAddr,
		Route: skydns.Range,
	}
	for {
		if err := tun.establishReq(req); err != nil {
			log.WithError(err).Debug("skydns: no tunnel yet")
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(skyDNSRetry):
			}
			continue
		}
		log.Info("skydns: answering .dmsg and .skynet names")
		if serveSkyDNSTUN(ctx, tun, sky) {
			return nil
		}
		log.Info("skydns: SkyVPN took the tunnel, waiting for it to end")
	}
}

// serveSkyDNSTUN feeds the engine from the tunnel until ctx ends, which drops
// the tunnel and reports true, or until another tunnel takes its place.
func serveSkyDNSTUN(ctx context.Context, tun *androidTUN, sky *skydns.Engine) (stopped bool) {
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, TUNMTU+4)
		for {
			n, err := tun.Read(buf)
			if err != nil {
				return
			}
			if skydns.Owns(buf[:n]) {
				_, _ = sky.Write(buf[:n]) //nolint:errcheck
			}
		}
	}()

	check := time.NewTicker(skyDNSCheck)
	defer check.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = tun.down() //nolint:errcheck
			<-readerDone
			return true
		case <-readerDone:
			tun.forget()
			return false
		case <-check.C:
			if !tun.holds() {
				tun.forget()
				<-readerDone
				return false
			}
		}
	}
}
