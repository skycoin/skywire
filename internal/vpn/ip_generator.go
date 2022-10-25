// Package vpn internal/vpn/ip_generator.go
package vpn

import (
	"errors"
	"net"
	"sync"
)

// IPGenerator is used to generate IPs for TUN interfaces.
type IPGenerator struct {
	mx           sync.Mutex
	currentRange int
	ranges       []*subnetIPIncrementer
}

// NewIPGenerator creates IP generator.
func NewIPGenerator() *IPGenerator {
	return &IPGenerator{
		ranges: []*subnetIPIncrementer{
			// exclude some most commonly used addresses in local networks
			newSubnetIPIncrementer([4]uint8{192, 168, 2, 0}, [4]uint8{192, 168, 255, 255}, 8),
			newSubnetIPIncrementer([4]uint8{172, 16, 0, 0}, [4]uint8{172, 31, 255, 255}, 8),
			newSubnetIPIncrementer([4]uint8{10, 0, 0, 0}, [4]uint8{10, 255, 255, 255}, 8),
		},
	}
}

// Reserve reserves `ip` so it will be excluded from the IP generation.
func (g *IPGenerator) Reserve(ip net.IP) error {
	octets, err := fetchIPv4Octets(ip)
	if err != nil {
		return err
	}

	// of course it's best to reserve it within the range it belongs to.
	// but it really doesn't matter, we may just reserve it in all incrementing instances,
	// that is much simpler and works anyway
	for _, inc := range g.ranges {
		inc.reserve(octets)
	}

	return nil
}

// Next gets next available IP.
func (g *IPGenerator) Next() (net.IP, error) {
	g.mx.Lock()
	defer g.mx.Unlock()

	for i := g.currentRange + 1; i != g.currentRange; i++ {
		if i >= len(g.ranges) {
			i = 0
		}

		ip, err := g.ranges[i].next()
		if err != nil {
			continue
		}

		return ip, nil
	}

	return nil, errors.New("no free IPs left")
}

func fetchIPv4Octets(ip net.IP) ([4]uint8, error) {
	ip = ip.To4()
	if ip == nil {
		return [4]uint8{}, errors.New("address is not of v4")
	}

	return [4]uint8{ip[0], ip[1], ip[2], ip[3]}, nil
}
