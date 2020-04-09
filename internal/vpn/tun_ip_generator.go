package vpn

import (
	"math"
	"net"
	"sync"
)

type TUNIPGenerator struct {
	mx        sync.Mutex
	currentIP uint8
	reserved  [math.MaxUint8]uint8
}

func NewTUNIPGenerator() *TUNIPGenerator {
	return &TUNIPGenerator{}
}

func (g *TUNIPGenerator) NextIP() (net.IP, net.IP, error) {

}
