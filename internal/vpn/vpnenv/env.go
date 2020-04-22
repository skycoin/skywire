package vpnenv

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/SkycoinProject/dmsg/cipher"
)

const (
	DmsgAddrsCountEnvKey = "DMSG_SRV_COUNT"
	DmsgAddrEnvPrefix    = "ADDR_DMSG_SRV_"

	DmsgDiscAddrEnvKey      = "ADDR_DMSG_DISC"
	TPDiscAddrEnvKey        = "ADDR_TP_DISC"
	RFAddrEnvKey            = "ADDR_RF"
	UptimeTrackerAddrEnvKey = "ADDR_UPTIME_TRACKER"

	STCPTableLenEnvKey = "STCP_TABLE_LEN"
	STCPKeyEnvPrefix   = "STCP_TABLE_KEY_"
	STCPValueEnvPrefix = "STCP_TABLE_"

	HypervisorsCountEnvKey  = "HYPERVISOR_COUNT"
	HypervisorAddrEnvPrefix = "ADDR_HYPERVISOR_"
)

// TODO: refactor package, temporary solution

func AppEnvArgs(dmsgDiscovery, tpDiscovery, rf, uptimeTracker string,
	stcpTable map[cipher.PubKey]string, hypervisors, dmsgSrvAddrs []string) map[string]string {
	envs := make(map[string]string)

	if dmsgDiscovery != "" {
		envs[DmsgDiscAddrEnvKey] = dmsgDiscovery
	}

	if tpDiscovery != "" {
		envs[TPDiscAddrEnvKey] = tpDiscovery
	}

	if rf != "" {
		envs[RFAddrEnvKey] = rf
	}

	if uptimeTracker != "" {
		envs[UptimeTrackerAddrEnvKey] = uptimeTracker
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

	if len(dmsgSrvAddrs) != 0 {
		envs[DmsgAddrsCountEnvKey] = strconv.FormatInt(int64(len(dmsgSrvAddrs)), 10)

		for i := range dmsgSrvAddrs {
			envs[DmsgAddrEnvPrefix+strconv.FormatInt(int64(i), 10)] = dmsgSrvAddrs[i]
		}
	}

	return envs
}

func IPFromEnv(key string) (net.IP, bool, error) {
	addr := os.Getenv(key)
	if addr == "" {
		return nil, false, nil
	}

	// in case whole URL is passed with the scheme
	if strings.Contains(addr, "://") {
		url, err := url.Parse(addr)
		if err == nil {
			addr = url.Host
		}
	}

	// filter out port if it exists
	if strings.Contains(addr, ":") {
		addr = strings.Split(addr, ":")[0]
	}

	ip := net.ParseIP(addr)
	if ip != nil {
		return ip, true, nil
	}

	// got domain instead of IP, need to resolve
	ips, err := net.LookupIP(addr)
	if err != nil {
		return nil, false, err
	}
	if len(ips) == 0 {
		return nil, false, fmt.Errorf("couldn't resolve IPs of %s", addr)
	}

	// initially take just the first one
	ip = ips[0]

	return ip, true, nil
}
