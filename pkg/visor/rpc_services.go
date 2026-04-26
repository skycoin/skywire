// Package visor pkg/visor/rpc.go
package visor

import (
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

// VPNServers gets available public VPN server from service discovery URL
func (r *RPC) VPNServers(vc *FilterServersIn, out *[]servicedisc.Service) (err error) {
	defer rpcutil.LogCall(r.log, "VPNServers", nil)(out, &err)
	vpnServers, err := r.visor.VPNServers(vc.Version, vc.Country)
	if vpnServers != nil {
		*out = vpnServers
	}
	return err
}

// ProxyServers gets available socks5 proxy servers from service discovery URL
func (r *RPC) ProxyServers(vc *FilterServersIn, out *[]servicedisc.Service) (err error) {
	defer rpcutil.LogCall(r.log, "ProxyServers", nil)(out, &err)
	proxyServers, err := r.visor.ProxyServers(vc.Version, vc.Country)
	if proxyServers != nil {
		*out = proxyServers
	}
	return err
}

// DeregisterService deregisters services from service discovery
func (r *RPC) DeregisterService(in *DeregisterServiceIn, out *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "DeregisterService", in)(out, &err)
	return r.visor.DeregisterService(in.PKs, in.ServiceType)
}

// TestProxy tests proxy servers by connecting through them.
func (r *RPC) TestProxy(conf ProxyTestConfig, out *[]ProxyTestResult) (err error) {
	defer rpcutil.LogCall(r.log, "TestProxy", conf)(out, &err)

	*out, err = r.visor.TestProxy(conf)
	return err
}
