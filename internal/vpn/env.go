package vpn

import (
	"strconv"

	"github.com/SkycoinProject/skywire-mainnet/pkg/visor"
)

const (
	DmsgDiscAddrEnvKey = "ADDR_DMSG_DISC"
	DmsgAddrEnvKey     = "ADDR_DMSG_SRV"
	TPDiscAddrEnvKey   = "ADDR_TP_DISC"
	RFAddrEnvKey       = "ADDR_RF"

	STCPTableLenEnvKey = "STCP_TABLE_LEN"
	STCPKeyEnvPrefix   = "STCP_TABLE_KEY_"
	STCPValueEnvPrefix = "STCP_TABLE_"

	HypervisorsCountEnvKey  = "HYPERVISOR_COUNT"
	HypervisorAddrEnvPrefix = "ADDR_HYPERVISOR_"
)

func AppEnvArgs(c visor.Config, dmsgSrvAddr string) map[string]string {
	envs := make(map[string]string)

	if c.Dmsg != nil {
		envs[DmsgDiscAddrEnvKey] = c.Dmsg.Discovery
	}

	if dmsgSrvAddr != "" {
		envs[DmsgAddrEnvKey] = dmsgSrvAddr
	}

	if c.Transport != nil {
		envs[TPDiscAddrEnvKey] = c.Transport.Discovery
	}

	if c.Routing != nil {
		envs[RFAddrEnvKey] = c.Routing.RouteFinder
	}

	if c.STCP != nil {
		envs[STCPTableLenEnvKey] = strconv.FormatInt(int64(len(c.STCP.PubKeyTable)), 10)

		itemIdx := 0
		for k, v := range c.STCP.PubKeyTable {
			envs[STCPKeyEnvPrefix+strconv.FormatInt(int64(itemIdx), 10)] = k.String()
			envs[STCPValueEnvPrefix+k.String()] = v
		}
	}

	if len(c.Hypervisors) != 0 {
		envs[HypervisorsCountEnvKey] = strconv.FormatInt(int64(len(c.Hypervisors)), 10)

		for i, h := range c.Hypervisors {
			envs[HypervisorAddrEnvPrefix+strconv.FormatInt(int64(i), 10)] = h.Addr
		}
	}

	return envs
}
