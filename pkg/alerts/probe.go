package alerts

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ProbePorts are the TCP ports LANProbe tries: ssh, http, https and the
// hypervisor UI.
var ProbePorts = []int{22, 80, 443, 8000}

// probeTimeout bounds each connect.
const probeTimeout = 3 * time.Second

// LANProbe reports whether ip answers on the local network. A port that
// accepts or refuses the connection both prove a live host, so no ICMP
// and no privileges are needed. Only private, link-local and loopback
// addresses are probed, so it cannot be used to scan the internet.
func LANProbe(ctx context.Context, ip string) (bool, error) {
	addr := net.ParseIP(ip)
	if addr == nil {
		return false, fmt.Errorf("invalid IP %q", ip)
	}
	if !addr.IsPrivate() && !addr.IsLinkLocalUnicast() && !addr.IsLoopback() {
		return false, fmt.Errorf("%s is not a LAN address", ip)
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	answers := make(chan bool, len(ProbePorts))
	for _, port := range ProbePorts {
		go func(port int) {
			var d net.Dialer
			c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
			if err == nil {
				_ = c.Close() //nolint:errcheck
			}
			// Windows words a refusal differently from ECONNREFUSED.
			answers <- err == nil || errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(err.Error(), "refused")
		}(port)
	}
	for range ProbePorts {
		if <-answers {
			return true, nil
		}
	}
	return false, nil
}
