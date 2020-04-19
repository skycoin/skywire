package vpn

import (
	"errors"
	"net"
	"sync"
)

type IPIncrementer struct {
	mx                sync.Mutex
	octets            [4]uint8
	octetLowerBorders [4]uint8
	octetBorders      [4]uint8
	step              uint8
	reserved          map[[4]uint8]struct{}
}

func NewIPIncrementer(octetLowerBorders, octetBorders [4]uint8, step uint8) *IPIncrementer {
	return &IPIncrementer{
		mx:                sync.Mutex{},
		octets:            octetLowerBorders,
		octetLowerBorders: octetLowerBorders,
		octetBorders:      octetBorders,
		step:              step,
		reserved:          make(map[[4]uint8]struct{}),
	}
}

func (inc *IPIncrementer) Next() (ip, gateway net.IP, err error) {
	inc.mx.Lock()
	defer inc.mx.Unlock()

	var ipArr [4]uint8

	for i := 3; i >= 0; i-- {
		for k := 0; k < i; k++ {
			ipArr[k] = inc.octets[k]
		}
		for k := i + 1; k < 4; k++ {
			ipArr[k] = inc.octets[k]
		}

		for j := inc.octets[i] + inc.step; j != inc.octets[i]; j += inc.step {
			if j >= inc.octetBorders[i] {
				j = inc.octetLowerBorders[i]
				continue
			}

			if i == 3 && j == 0 {
				// TODO: fix to skip only the network address
				continue
			}

			ipArr[i] = j
			if _, ok := inc.reserved[ipArr]; !ok {
				inc.octets[i] = j
				inc.reserved[ipArr] = struct{}{}

				// TODO: fix possible miscalculations
				return net.IPv4(inc.octets[0], inc.octets[1], inc.octets[2], inc.octets[3]), net.IPv4(inc.octets[0], inc.octets[1], inc.octets[2], inc.octets[3]-1), nil
			}
		}

		inc.octets[i] = inc.octetLowerBorders[i]
	}

	return nil, nil, errors.New("no free IPs left")
}

type TUNIPGenerator struct {
	mx           sync.Mutex
	currentRange uint8
	ranges       []*IPIncrementer
}

func NewTUNIPGenerator() *TUNIPGenerator {
	return &TUNIPGenerator{
		ranges: []*IPIncrementer{
			NewIPIncrementer([4]uint8{192, 168, 0, 0}, [4]uint8{192, 168, 255, 255}, 4),
			NewIPIncrementer([4]uint8{172, 16, 0, 0}, [4]uint8{172, 31, 255, 255}, 4),
			NewIPIncrementer([4]uint8{10, 0, 0, 0}, [4]uint8{10, 255, 255, 255}, 4),
		},
	}
}

func (g *TUNIPGenerator) Next() (ip, gateway net.IP, err error) {
	g.mx.Lock()
	defer g.mx.Unlock()

	for i := g.currentRange; i < 4; i++ {
		ip, gateway, err := g.ranges[i].Next()
		if err != nil {
			g.currentRange++
			continue
		}

		return ip, gateway, nil
	}

	return nil, nil, errors.New("no free IPs left")
}
