package vpn

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
	// DmsgAddrsCountEnvKey is env arg holding Dmsg servers count.
	DmsgAddrsCountEnvKey = "DMSG_SRV_COUNT"
	// DmsgAddrEnvPrefix is prefix for each env arg holding Dmsg server address.
	DmsgAddrEnvPrefix = "ADDR_DMSG_SRV_"

	// DmsgDiscAddrEnvKey is env arg holding Dmsg discovery address.
	DmsgDiscAddrEnvKey = "ADDR_DMSG_DISC"
	// TPDiscAddrEnvKey is env arg holding TP discovery address.
	TPDiscAddrEnvKey = "ADDR_TP_DISC"
	// RFAddrEnvKey is env arg holding RF address.
	RFAddrEnvKey = "ADDR_RF"
	// UptimeTrackerAddrEnvKey is env arg holding uptime tracker address.
	UptimeTrackerAddrEnvKey = "ADDR_UPTIME_TRACKER"

	// STCPTableLenEnvKey is env arg holding Stcp table length.
	STCPTableLenEnvKey = "STCP_TABLE_LEN"
	// STCPKeyEnvPrefix is prefix for each env arg holding STCP entity key.
	STCPKeyEnvPrefix = "STCP_TABLE_KEY_"
	// STCPValueEnvPrefix is prefix for each env arg holding STCP entity value.
	STCPValueEnvPrefix = "STCP_TABLE_"
)

// AppEnvArgs forms env args to pass to the app process.
func AppEnvArgs(dmsgDiscovery, tpDiscovery, rf, uptimeTracker string,
	stcpTable map[cipher.PubKey]string, dmsgSrvAddrs []string) map[string]string {
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

	if len(dmsgSrvAddrs) != 0 {
		envs[DmsgAddrsCountEnvKey] = strconv.FormatInt(int64(len(dmsgSrvAddrs)), 10)

		for i := range dmsgSrvAddrs {
			envs[DmsgAddrEnvPrefix+strconv.FormatInt(int64(i), 10)] = dmsgSrvAddrs[i]
		}
	}

	return envs
}

// IPFromEnv gets IP address from the env arg `key`. Env value may be one of:
// - full URL with port;
// - full URL without port;
// - domain with port;
// - domain without port;
// - IP with port;
// - IP without port.
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
		return nil, false, fmt.Errorf("error resolving IPs of %s", addr)
	}

	// initially take just the first one
	ip = ips[0]

	return ip, true, nil
}
