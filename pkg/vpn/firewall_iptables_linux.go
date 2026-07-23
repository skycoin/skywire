//go:build linux && !nftfw
// +build linux,!nftfw

// Package vpn pkg/vpn/firewall_iptables_linux.go c4-app-vpn
//
// The default firewall backend: iptables shell-outs, identical to what the
// router has always done. Each installed rule's full argv is recorded so
// teardown can remove it with the matching `-D`. This is the fleet path; the
// pure-Go nftables backend (firewall_nftfw_linux.go) replaces it only under the
// `nftfw` build tag.
package vpn

import (
	"net"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/util/osutil"
)

type iptablesFirewall struct {
	log   logrus.FieldLogger
	added [][]string // full iptables argv (incl. -t <table> -A <chain>) per rule
}

// newFirewall builds the iptables backend. It never fails (rules are added
// lazily); errors surface per-operation.
func newFirewall(log logrus.FieldLogger) (firewall, error) {
	return &iptablesFirewall{log: log}, nil
}

// run installs one rule and records its argv for teardown.
func (f *iptablesFirewall) run(argv []string) error {
	if err := osutil.RunElevated("iptables", argv...); err != nil {
		return err
	}
	f.added = append(f.added, argv)
	return nil
}

func (f *iptablesFirewall) masquerade(oif string) error {
	return f.run([]string{"-t", "nat", "-A", "POSTROUTING", "-o", oif, "-j", "MASQUERADE"})
}

func (f *iptablesFirewall) forwardAccept(iif, oif string, stateful bool) error {
	argv := []string{"-A", "FORWARD", "-i", iif, "-o", oif}
	if stateful {
		argv = append(argv, "-m", "state", "--state", "RELATED,ESTABLISHED")
	}
	argv = append(argv, "-j", "ACCEPT")
	return f.run(argv)
}

func (f *iptablesFirewall) clampMSS(oif string) error {
	return f.run([]string{
		"-t", "mangle", "-A", "FORWARD", "-o", oif,
		"-p", "tcp", "-m", "tcp", "--tcp-flags", "SYN,RST", "SYN",
		"-j", "TCPMSS", "--clamp-mss-to-pmtu",
	})
}

func (f *iptablesFirewall) redirectTCP(hook fwHook, iif string, dst *net.IPNet, toPort uint16) error {
	chain := "PREROUTING"
	if hook == fwOutput {
		chain = "OUTPUT"
	}
	argv := []string{"-t", "nat", "-A", chain}
	if iif != "" {
		argv = append(argv, "-i", iif)
	}
	argv = append(argv, "-d", dst.String(), "-p", "tcp",
		"-j", "REDIRECT", "--to-ports", strconv.Itoa(int(toPort)))
	return f.run(argv)
}

// teardown removes each rule (newest first) by flipping its `-A` to `-D`.
func (f *iptablesFirewall) teardown() {
	for i := len(f.added) - 1; i >= 0; i-- {
		del := deleteArgv(f.added[i])
		if err := osutil.RunElevated("iptables", del...); err != nil {
			f.log.WithError(err).Warnf("firewall teardown: remove %s", strings.Join(f.added[i], " "))
		}
	}
	f.added = nil
}

// deleteArgv returns argv with the first "-A"/"-I" token replaced by "-D".
func deleteArgv(argv []string) []string {
	out := append([]string(nil), argv...)
	for i, tok := range out {
		if tok == "-A" || tok == "-I" {
			out[i] = "-D"
			break
		}
	}
	return out
}
