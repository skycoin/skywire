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
}

func NewTUNIPGenerator() *TUNIPGenerator {
	return &TUNIPGenerator{}
}

func (g *TUNIPGenerator) Next() (ip, gateway net.IP, err error) {
	g.mx.Lock()
	defer g.mx.Unlock()

	for i := g.currentIP + 2; i != g.currentIP; i += 2 {
		if !g.reserved[i] {
			g.currentIP = i
			return net.IPv4(192, 168, 255, g.currentIP+1), net.IPv4(192, 168, 255, g.currentIP), nil
		}
	}

	return nil, nil, errors.New("no free IPs left")
}
