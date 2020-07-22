package skywiremob

import (
	"fmt"
	"net"
	"strings"
)

func IPs() string {
	ips, err := net.LookupIP("address.resolver.skywire.cc")
	if err != nil {
		fmt.Printf("DICK : PANIC: %v\n", err)
		return ""
	}
	ipsStr := make([]string, 0, len(ips))
	for _, ip := range ips {
		ipsStr = append(ipsStr, ip.String())
	}

	return strings.Join(ipsStr, ";")
}
