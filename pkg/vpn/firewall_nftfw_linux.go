//go:build linux && nftfw
// +build linux,nftfw

// Package vpn pkg/vpn/firewall_nftfw_linux.go c4-app-vpn
//
// The `nftfw` firewall backend: delegates to netctl.NFTFirewall (pure-Go
// nftables). Selected only under the `nftfw` build tag — the gokrazy appliance
// image, which ships no iptables binary. All rules land in a single `skywire`
// nftables table; teardown drops the table.
package vpn

import (
	"net"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/vpn/netctl"
)

type nftFirewall struct {
	log logrus.FieldLogger
	fw  *netctl.NFTFirewall
}

// newFirewall builds the skywire nftables table + base chains.
func newFirewall(log logrus.FieldLogger) (firewall, error) {
	fw, err := netctl.NewNFTFirewall()
	if err != nil {
		return nil, err
	}
	return &nftFirewall{log: log, fw: fw}, nil
}

func (n *nftFirewall) masquerade(oif string) error { return n.fw.Masquerade(oif) }

func (n *nftFirewall) forwardAccept(iif, oif string, stateful bool) error {
	return n.fw.ForwardAccept(iif, oif, stateful)
}

func (n *nftFirewall) clampMSS(oif string) error { return n.fw.ClampMSS(oif) }

func (n *nftFirewall) redirectTCP(hook fwHook, iif string, dst *net.IPNet, toPort uint16) error {
	h := netctl.HookPrerouting
	if hook == fwOutput {
		h = netctl.HookOutput
	}
	return n.fw.RedirectTCP(h, iif, dst, toPort)
}

func (n *nftFirewall) teardown() {
	if err := n.fw.Teardown(); err != nil {
		n.log.WithError(err).Warn("firewall teardown: drop skywire nft table")
	}
}
