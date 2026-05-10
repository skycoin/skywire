// Package visor pkg/visor/embedded_resolver_tls.go
//
// Helpers shared by EmbeddedDmsgWeb and EmbeddedSkynetWeb to wire
// optional TLS MITM mode onto their underlying runtime configs.
//
// Trust model and threat analysis live in pkg/skynetca; the helpers
// here only handle the visor-side bookkeeping: load the CA from the
// configured paths, build a LeafMinter, mutate the runtime config.
// All errors are non-fatal — when CA load fails the resolver
// continues to run in non-MITM mode and the caller logs the reason.
package visor

import (
	"errors"
	"fmt"

	"github.com/skycoin/skywire/pkg/dmsgweb"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skynetca"
	"github.com/skycoin/skywire/pkg/skynetweb"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// wireSkynetTLSMITM populates cfg with a LeafMinter loaded from
// vcfg.TLSCAPath / vcfg.TLSCAKeyPath. Returns an error if the CA
// cannot be loaded; cfg is left untouched in that case so the
// resolver runs in plain (non-MITM) mode.
func wireSkynetTLSMITM(cfg *skynetweb.Config, vcfg *visorconfig.SkynetWebConfig, log *logging.Logger) error {
	if vcfg.TLSCAPath == "" || vcfg.TLSCAKeyPath == "" {
		return errors.New("tls_ca_path and tls_ca_key_path must both be set")
	}
	ca, key, err := skynetca.LoadCA(vcfg.TLSCAPath, vcfg.TLSCAKeyPath)
	if err != nil {
		return fmt.Errorf("load CA: %w", err)
	}
	cfg.TLSMITM = true
	cfg.TLSPort = vcfg.TLSPort
	cfg.LeafMinter = skynetca.NewMinter(ca, key, skynetca.LeafOptions{})
	log.WithField("ca_fingerprint", skynetca.Fingerprint(ca)).
		Info("skynetweb TLS MITM enabled")
	return nil
}

// wireDmsgTLSMITM is the dmsgweb counterpart of wireSkynetTLSMITM.
// Same semantics; separate symbol to keep the runtime configs
// independently typed.
func wireDmsgTLSMITM(cfg *dmsgweb.Config, vcfg *visorconfig.DmsgWebConfig, log *logging.Logger) error {
	if vcfg.TLSCAPath == "" || vcfg.TLSCAKeyPath == "" {
		return errors.New("tls_ca_path and tls_ca_key_path must both be set")
	}
	ca, key, err := skynetca.LoadCA(vcfg.TLSCAPath, vcfg.TLSCAKeyPath)
	if err != nil {
		return fmt.Errorf("load CA: %w", err)
	}
	cfg.TLSMITM = true
	cfg.TLSPort = vcfg.TLSPort
	cfg.LeafMinter = skynetca.NewMinter(ca, key, skynetca.LeafOptions{})
	log.WithField("ca_fingerprint", skynetca.Fingerprint(ca)).
		Info("dmsgweb TLS MITM enabled")
	return nil
}
