package vpnenv

import (
	"strconv"

	"github.com/SkycoinProject/dmsg/cipher"
)

const (
	DmsgDiscAddrEnvKey   = "ADDR_DMSG_DISC"
	DmsgAddrsCountEnvKey = "DMSG_SRV_COUNT"
	DmsgAddrEnvPrefix    = "ADDR_DMSG_SRV_"
	TPDiscAddrEnvKey     = "ADDR_TP_DISC"
	RFAddrEnvKey         = "ADDR_RF"

	STCPTableLenEnvKey = "STCP_TABLE_LEN"
	STCPKeyEnvPrefix   = "STCP_TABLE_KEY_"
	STCPValueEnvPrefix = "STCP_TABLE_"

	HypervisorsCountEnvKey  = "HYPERVISOR_COUNT"
	HypervisorAddrEnvPrefix = "ADDR_HYPERVISOR_"
)

// TODO: refactor package, temporary solution

func AppEnvArgs(dmsgDiscovery, tpDiscovery, rf string,
	stcpTable map[cipher.PubKey]string, hypervisors, dmsgSrvAddrs []string) map[string]string {
	envs := make(map[string]string)

	if dmsgDiscovery != "" {
		envs[DmsgDiscAddrEnvKey] = dmsgDiscovery
	}

	if len(dmsgSrvAddrs) != 0 {
		envs[DmsgAddrsCountEnvKey] = strconv.FormatInt(int64(len(dmsgSrvAddrs)), 10)

		for i := range dmsgSrvAddrs {
			envs[DmsgAddrEnvPrefix+strconv.FormatInt(int64(i), 10)] = dmsgSrvAddrs[i]
		}
	}

	if tpDiscovery != "" {
		envs[TPDiscAddrEnvKey] = tpDiscovery
	}

	if rf != "" {
		envs[RFAddrEnvKey] = rf
	}

	if len(stcpTable) != 0 {
		envs[STCPTableLenEnvKey] = strconv.FormatInt(int64(len(stcpTable)), 10)

		itemIdx := 0
		for k, v := range stcpTable {
			envs[STCPKeyEnvPrefix+strconv.FormatInt(int64(itemIdx), 10)] = k.String()
			envs[STCPValueEnvPrefix+k.String()] = v
		}
	}

	if len(hypervisors) != 0 {
		envs[HypervisorsCountEnvKey] = strconv.FormatInt(int64(len(hypervisors)), 10)

		for i, h := range hypervisors {
			envs[HypervisorAddrEnvPrefix+strconv.FormatInt(int64(i), 10)] = h
		}
	}

	/*if c.Dmsg != nil {
		envs[DmsgDiscAddrEnvKey] = c.Dmsg.Discovery
	}

	if len(dmsgSrvAddrs) != 0 {
		envs[DmsgAddrsCountEnvKey] = strconv.FormatInt(int64(len(dmsgSrvAddrs)), 10)

		for i := range dmsgSrvAddrs {
			envs[DmsgAddrEnvPrefix+strconv.FormatInt(int64(i), 10)] = dmsgSrvAddrs[i]
		}
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
	}*/

	return envs
}
