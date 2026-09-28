// Package mobilecore pkg/mobilecore/genconfig.go c4-vis-core
package mobilecore

import (
	"errors"
	"strconv"
	"strings"

	cliconfig "github.com/skycoin/skywire/cmd/skywire-cli/commands/config"
)

// GenOptions are the `config gen` options the phone generates its config
// with. PhoneGenOptions fills them with exactly the argv Android's
// ConfigManager.genArgs passes; the post-generation edits (the phone
// profile) are the app's job on both platforms, not this package's.
type GenOptions struct {
	// OutPath is the config file to write (-o). Required.
	OutPath string `json:"out_path"`
	// Regen keeps the identity of an existing config at OutPath (-r); safe
	// on a first run too.
	Regen bool `json:"regen"`
	// Hypervisor enables the local API (-i).
	Hypervisor bool `json:"hypervisor"`
	// Auth puts the local API behind a login (--auth).
	Auth bool `json:"auth"`
	// HypervisorAddr is the local API's listen address (--hvaddr).
	HypervisorAddr string `json:"hv_addr"`
	// DisablePublicAutoconnect turns off autoconnect to public visors
	// (--autoconn).
	DisablePublicAutoconnect bool `json:"disable_public_autoconnect"`
	// ServeChat, ServeProxy and ServeVPN autostart skychat, the SOCKS
	// server and the VPN server (--servechat, --serveproxy, --servevpn).
	ServeChat  bool `json:"serve_chat"`
	ServeProxy bool `json:"serve_proxy"`
	ServeVPN   bool `json:"serve_vpn"`
	// DisableApps leaves these apps out of the launcher (--disableapps).
	DisableApps []string `json:"disable_apps"`
	// BinPath is the launcher's bin_path (--binpath).
	BinPath string `json:"bin_path"`
	// NoFetch keeps generation offline: the embedded deployment's service
	// endpoints instead of the conf service's (--nofetch).
	NoFetch bool `json:"no_fetch"`
}

// PhoneGenOptions returns the options Android generates its config with,
// writing to outPath with the launcher's bin_path at binPath.
func PhoneGenOptions(outPath, binPath string) GenOptions {
	return GenOptions{
		OutPath:                  outPath,
		Regen:                    true,
		Hypervisor:               true,
		Auth:                     true,
		HypervisorAddr:           "127.0.0.1:8000",
		DisablePublicAutoconnect: true,
		DisableApps:              []string{"skysocks", "vpn-server", "vpn-router", "skydex-market", "skycoin-web"},
		BinPath:                  binPath,
		NoFetch:                  true,
	}
}

// Args returns the `config gen` argv for o, in the order and form Android
// passes it (ConfigManager.genArgs), without the leading "config gen".
func (o GenOptions) Args() []string {
	var args []string
	if o.Regen {
		args = append(args, "-r")
	}
	args = append(args, "-o", o.OutPath, "-w")
	if o.Hypervisor {
		args = append(args, "-i")
	}
	if o.Auth {
		args = append(args, "--auth")
	}
	if o.HypervisorAddr != "" {
		args = append(args, "--hvaddr", o.HypervisorAddr)
	}
	if o.DisablePublicAutoconnect {
		args = append(args, "--autoconn")
	}
	args = append(args,
		"--servechat="+strconv.FormatBool(o.ServeChat),
		"--serveproxy="+strconv.FormatBool(o.ServeProxy),
		"--servevpn="+strconv.FormatBool(o.ServeVPN),
	)
	if len(o.DisableApps) > 0 {
		args = append(args, "--disableapps", strings.Join(o.DisableApps, ","))
	}
	if o.BinPath != "" {
		args = append(args, "--binpath", o.BinPath)
	}
	if o.NoFetch {
		args = append(args, "--nofetch")
	}
	return args
}

// GenConfig writes a config with `config gen`, run inside this process
// (cliconfig.RunGen): iOS cannot exec the core the way Android runs
// `libskywire-mobile.so config gen`. Same command, same file.
func GenConfig(o GenOptions) error {
	if o.OutPath == "" {
		return errors.New("mobilecore: no config output path")
	}
	return cliconfig.RunGen(o.Args())
}
