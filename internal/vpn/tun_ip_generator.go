package vpn

import (
	"errors"
	"math"
	"net"
	"sync"
)

type TUNIPGenerator struct {
	mx        sync.Mutex
	currentIP uint8
	reserved  [math.MaxUint8]bool
	step      uint8
}

func NewTUNIPGenerator(step uint8) *TUNIPGenerator {
	return &TUNIPGenerator{
		step:      step,
		currentIP: 1,
	}
}

func (g *TUNIPGenerator) Next() (ip, gateway net.IP, err error) {
	g.mx.Lock()
	defer g.mx.Unlock()

	for i := g.currentIP + g.step; i != g.currentIP; i += g.step {
		if i == 0 {
			// skip 192.168.255.0
			continue
		}

		if !g.reserved[i] {
			g.currentIP = i
			g.reserved[i] = true
			return net.IPv4(192, 168, 255, g.currentIP+1), net.IPv4(192, 168, 255, g.currentIP), nil
		}
	}

	return nil, nil, errors.New("no free IPs left")
}
