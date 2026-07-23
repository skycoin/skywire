//go:build nftfw && linux
// +build nftfw,linux

// Package netctl pkg/vpn/netctl/firewall_nft_linux.go c4-app-vpn
//
// NFTFirewall is a pure-Go (google/nftables) replacement for the vpn-router /
// vpn-client iptables shell-outs — masquerade, FORWARD accept, the mesh-gateway
// REDIRECT, and the TCPMSS clamp. It exists so skywire's router can run on a
// userland-less appliance (gokrazy) that ships no iptables/nft binary.
//
// It is BUILD-TAG GATED (`nftfw`): normal builds keep the proven iptables path,
// because native-nftables rules and a host's existing iptables firewall can
// interact in subtle ways (e.g. a FORWARD drop policy in another base chain).
// The appliance image — a clean, skywire-only environment — builds with `nftfw`.
//
// All rules live in a dedicated `skywire` table; Teardown deletes the whole
// table, so cleanup can't leave stragglers in the system chains.
package netctl

import (
	"encoding/binary"
	"fmt"
	"net"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

// NFTFirewall owns the skywire nftables table and its base chains.
type NFTFirewall struct {
	c           *nftables.Conn
	table       *nftables.Table
	postrouting *nftables.Chain // nat, srcnat priority — masquerade
	prerouting  *nftables.Chain // nat, dstnat priority — REDIRECT (router/LAN)
	output      *nftables.Chain // nat, dstnat priority — REDIRECT (client/local)
	forward     *nftables.Chain // filter — FORWARD accept
	mangleFwd   *nftables.Chain // filter, mangle priority — TCPMSS clamp
}

// NewNFTFirewall builds the skywire table + base chains and commits them.
func NewNFTFirewall() (*NFTFirewall, error) {
	c, err := nftables.New()
	if err != nil {
		return nil, fmt.Errorf("netctl: nftables conn: %w", err)
	}
	fw := &NFTFirewall{c: c}
	fw.table = c.AddTable(&nftables.Table{Family: nftables.TableFamilyIPv4, Name: "skywire"})
	fw.postrouting = c.AddChain(&nftables.Chain{
		Name: "postrouting", Table: fw.table, Type: nftables.ChainTypeNAT,
		Hooknum: nftables.ChainHookPostrouting, Priority: nftables.ChainPriorityNATSource,
	})
	fw.prerouting = c.AddChain(&nftables.Chain{
		Name: "prerouting", Table: fw.table, Type: nftables.ChainTypeNAT,
		Hooknum: nftables.ChainHookPrerouting, Priority: nftables.ChainPriorityNATDest,
	})
	fw.output = c.AddChain(&nftables.Chain{
		Name: "output", Table: fw.table, Type: nftables.ChainTypeNAT,
		Hooknum: nftables.ChainHookOutput, Priority: nftables.ChainPriorityNATDest,
	})
	fw.forward = c.AddChain(&nftables.Chain{
		Name: "forward", Table: fw.table, Type: nftables.ChainTypeFilter,
		Hooknum: nftables.ChainHookForward, Priority: nftables.ChainPriorityFilter,
	})
	fw.mangleFwd = c.AddChain(&nftables.Chain{
		Name: "mangle_fwd", Table: fw.table, Type: nftables.ChainTypeFilter,
		Hooknum: nftables.ChainHookForward, Priority: nftables.ChainPriorityMangle,
	})
	if err := c.Flush(); err != nil {
		return nil, fmt.Errorf("netctl: create skywire nft table: %w", err)
	}
	return fw, nil
}

// Teardown deletes the skywire table (and everything in it).
func (fw *NFTFirewall) Teardown() error {
	fw.c.DelTable(fw.table)
	if err := fw.c.Flush(); err != nil {
		return fmt.Errorf("netctl: delete skywire nft table: %w", err)
	}
	return nil
}

// ifname null-pads an interface name to IFNAMSIZ (16) for an nftables comparison.
func ifname(name string) []byte {
	b := make([]byte, 16)
	copy(b, name)
	return b
}

// matchIif / matchOif compare the in/out interface.
func matchIif(name string) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ifname(name)},
	}
}

func matchOif(name string) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ifname(name)},
	}
}

// matchL4Proto matches an IP protocol number (unix.IPPROTO_TCP/UDP).
func matchL4Proto(proto byte) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{proto}},
	}
}

// matchDaddr matches the IPv4 destination against a CIDR.
func matchDaddr(n *net.IPNet) []expr.Any {
	ip := n.IP.To4()
	mask := net.IP(n.Mask).To4()
	load := &expr.Payload{
		DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4, // ip daddr
	}
	if ones, _ := n.Mask.Size(); ones == 32 {
		return []expr.Any{load, &expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ip}}
	}
	return []expr.Any{
		load,
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 4, Mask: mask, Xor: []byte{0, 0, 0, 0}},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ip.Mask(n.Mask)},
	}
}

// matchDport matches an L4 destination port.
func matchDport(port uint16) []expr.Any {
	p := make([]byte, 2)
	binary.BigEndian.PutUint16(p, port)
	return []expr.Any{
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: p},
	}
}

// Masquerade SNATs traffic leaving oif (≡ `-t nat -A POSTROUTING -o oif -j MASQUERADE`).
func (fw *NFTFirewall) Masquerade(oif string) error {
	fw.c.AddRule(&nftables.Rule{
		Table: fw.table, Chain: fw.postrouting,
		Exprs: append(matchOif(oif), &expr.Masq{}),
	})
	return fw.commit("masquerade %s", oif)
}

// ForwardAccept accepts iif→oif forwarded traffic (≡ `-A FORWARD -i iif -o oif
// -j ACCEPT`). When stateful, it additionally requires ct state
// established,related (the return-path rule).
func (fw *NFTFirewall) ForwardAccept(iif, oif string, stateful bool) error {
	exprs := append(matchIif(iif), matchOif(oif)...)
	if stateful {
		exprs = append(exprs,
			&expr.Ct{Key: expr.CtKeySTATE, Register: 1},
			&expr.Bitwise{
				SourceRegister: 1, DestRegister: 1, Len: 4,
				Mask: ctStateMask(expr.CtStateBitESTABLISHED | expr.CtStateBitRELATED),
				Xor:  []byte{0, 0, 0, 0},
			},
			&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: []byte{0, 0, 0, 0}},
		)
	}
	exprs = append(exprs, &expr.Verdict{Kind: expr.VerdictAccept})
	fw.c.AddRule(&nftables.Rule{Table: fw.table, Chain: fw.forward, Exprs: exprs})
	return fw.commit("forward accept %s→%s", iif, oif)
}

func ctStateMask(bits uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, bits)
	return b
}

// ClampMSS clamps forwarded TCP SYNs' MSS to the path MTU on oif (≡ `-t mangle -A
// FORWARD -o oif -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu`).
// nftables expresses "clamp to pmtu" natively as `rt tcpmss`.
func (fw *NFTFirewall) ClampMSS(oif string) error {
	exprs := matchOif(oif)
	exprs = append(exprs, matchL4Proto(unix.IPPROTO_TCP)...)
	// tcp flags & (SYN|RST) == SYN
	exprs = append(exprs,
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 13, Len: 1},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 1,
			Mask: []byte{0x06}, Xor: []byte{0x00}}, // SYN(0x02)|RST(0x04)
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{0x02}}, // == SYN
		// reg1 = route MSS (clamp-to-pmtu), then set the TCP maxseg option to it.
		&expr.Rt{Key: expr.RtTCPMSS, Register: 1},
		&expr.Exthdr{SourceRegister: 1, Type: 2 /*TCPOPT_MAXSEG*/, Offset: 2, Len: 2, Op: expr.ExthdrOpTcpopt},
	)
	fw.c.AddRule(&nftables.Rule{Table: fw.table, Chain: fw.mangleFwd, Exprs: exprs})
	return fw.commit("mss clamp %s", oif)
}

// RedirectHook selects which nat chain a REDIRECT is installed in.
type RedirectHook int

const (
	// HookPrerouting redirects forwarded/inbound traffic (the vpn-router / LAN case).
	HookPrerouting RedirectHook = iota
	// HookOutput redirects locally-generated traffic (the vpn-client / host case).
	HookOutput
)

// RedirectTCP redirects TCP destined for dst (a CIDR) to a local port (≡ `-t nat
// -A <PRE|OUT> [-i iif] -d dst -p tcp -j REDIRECT --to-ports toPort`). iif is
// optional (empty = any).
func (fw *NFTFirewall) RedirectTCP(hook RedirectHook, iif string, dst *net.IPNet, toPort uint16) error {
	var exprs []expr.Any
	if iif != "" {
		exprs = append(exprs, matchIif(iif)...)
	}
	exprs = append(exprs, matchL4Proto(unix.IPPROTO_TCP)...)
	exprs = append(exprs, matchDaddr(dst)...)
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, toPort)
	exprs = append(exprs,
		&expr.Immediate{Register: 1, Data: port},
		&expr.Redir{RegisterProtoMin: 1, RegisterProtoMax: 1},
	)
	fw.c.AddRule(&nftables.Rule{Table: fw.table, Chain: fw.natChain(hook), Exprs: exprs})
	return fw.commit("redirect %s → :%d", dst, toPort)
}

// ReturnDPort short-circuits (RETURN) traffic to host:dport in the given hook,
// so it is NOT redirected — used to exempt the mesh resolver's own upstream DNS
// (≡ `-t nat -A OUTPUT -p <proto> -d host --dport dport -j RETURN`), preventing a
// redirect loop.
func (fw *NFTFirewall) ReturnDPort(hook RedirectHook, proto byte, host net.IP, dport uint16) error {
	exprs := matchL4Proto(proto)
	exprs = append(exprs, matchDaddr(&net.IPNet{IP: host.To4(), Mask: net.CIDRMask(32, 32)})...)
	exprs = append(exprs, matchDport(dport)...)
	exprs = append(exprs, &expr.Verdict{Kind: expr.VerdictReturn})
	fw.c.AddRule(&nftables.Rule{Table: fw.table, Chain: fw.natChain(hook), Exprs: exprs})
	return fw.commit("return %s %s:%d", protoName(proto), host, dport)
}

// RedirectDPort redirects traffic to any host on dport to a local port in the
// given hook (≡ `-A <chain> -p <proto> --dport dport -j REDIRECT --to-ports
// toPort`) — the mesh client's DNS interception.
func (fw *NFTFirewall) RedirectDPort(hook RedirectHook, proto byte, dport, toPort uint16) error {
	exprs := matchL4Proto(proto)
	exprs = append(exprs, matchDport(dport)...)
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, toPort)
	exprs = append(exprs,
		&expr.Immediate{Register: 1, Data: port},
		&expr.Redir{RegisterProtoMin: 1, RegisterProtoMax: 1},
	)
	fw.c.AddRule(&nftables.Rule{Table: fw.table, Chain: fw.natChain(hook), Exprs: exprs})
	return fw.commit("redirect %s :%d → :%d", protoName(proto), dport, toPort)
}

func (fw *NFTFirewall) natChain(hook RedirectHook) *nftables.Chain {
	if hook == HookOutput {
		return fw.output
	}
	return fw.prerouting
}

func (fw *NFTFirewall) commit(format string, args ...interface{}) error {
	if err := fw.c.Flush(); err != nil {
		return fmt.Errorf("netctl: nft "+format+": %w", append(args, err)...)
	}
	return nil
}

func protoName(p byte) string {
	if p == unix.IPPROTO_UDP {
		return "udp"
	}
	return "tcp"
}
