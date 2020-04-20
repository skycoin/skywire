package vpn

import (
	"errors"
	"net"
	"sync"
)

// SubnetIPIncrementer is used to increment over the subnet IP address
// between the specified borders.
type SubnetIPIncrementer struct {
	mx                sync.Mutex
	octets            [4]uint8
	octetLowerBorders [4]uint8
	octetBorders      [4]uint8
	step              uint8
	reserved          map[[4]uint8]struct{}
}

func NewSubnetIPIncrementer(octetLowerBorders, octetBorders [4]uint8, step uint8) *SubnetIPIncrementer {
	return &SubnetIPIncrementer{
		mx:                sync.Mutex{},
		octets:            octetLowerBorders,
		octetLowerBorders: octetLowerBorders,
		octetBorders:      octetBorders,
		step:              step,
		reserved:          make(map[[4]uint8]struct{}),
	}
}

func (inc *SubnetIPIncrementer) Next() (net.IP, error) {
	inc.mx.Lock()
	defer inc.mx.Unlock()

	var generatedIP [4]uint8

	o1 := inc.octets[0]
	o2 := inc.octets[1]
	o3 := inc.octets[2]
	for {
		for {
			for {
				generatedIP[0] = inc.octets[0]
				generatedIP[1] = inc.octets[1]
				generatedIP[2] = inc.octets[2]

				for o4 := inc.octets[3] + inc.step; o4 != inc.octets[3]; o4 += inc.step {
					if o4 >= inc.octetBorders[3] {
						o4 = inc.octetLowerBorders[3]
						continue
					}

					generatedIP[3] = o4

					if _, ok := inc.reserved[generatedIP]; !ok {
						inc.octets[3] = o4
						inc.reserved[generatedIP] = struct{}{}

						return net.IPv4(generatedIP[0], generatedIP[1], generatedIP[2], generatedIP[3]), nil
					}
				}

				inc.octets[3] = inc.octetLowerBorders[3]

				if inc.octets[2] == inc.octetBorders[2] {
					inc.octets[2] = inc.octetLowerBorders[2]
				} else {
					inc.octets[2]++
				}

				if inc.octets[2] == o3 {
					inc.octets[2] = inc.octetLowerBorders[2]
					break
				}
			}

			if inc.octets[1] == inc.octetBorders[1] {
				inc.octets[1] = inc.octetLowerBorders[1]
			} else {
				inc.octets[1]++
			}

			if inc.octets[1] == o2 {
				inc.octets[1] = inc.octetLowerBorders[1]
				break
			}
		}

		if inc.octets[0] == inc.octetBorders[0] {
			inc.octets[0] = inc.octetLowerBorders[0]
		} else {
			inc.octets[0]++
		}

		if inc.octets[0] == o1 {
			inc.octets[0] = inc.octetLowerBorders[0]
			break
		}
	}

	return nil, errors.New("no free IPs left")
}

func (inc *SubnetIPIncrementer) Reserve(octets [4]uint8) {
	inc.mx.Lock()
	defer inc.mx.Unlock()

	inc.reserved[octets] = struct{}{}
}

type TUNIPGenerator struct {
	mx           sync.Mutex
	currentRange int
	ranges       []*SubnetIPIncrementer
}

func NewTUNIPGenerator() *TUNIPGenerator {
	return &TUNIPGenerator{
		ranges: []*SubnetIPIncrementer{
			NewSubnetIPIncrementer([4]uint8{192, 168, 0, 0}, [4]uint8{192, 168, 255, 255}, 8),
			NewSubnetIPIncrementer([4]uint8{172, 16, 0, 0}, [4]uint8{172, 31, 255, 255}, 8),
			NewSubnetIPIncrementer([4]uint8{10, 0, 0, 0}, [4]uint8{10, 255, 255, 255}, 8),
		},
	}
}

func (g *TUNIPGenerator) Reserve(ip net.IP) error {
	octets, err := fetchIPv4Bytes(ip)
	if err != nil {
		return err
	}

	// of course it's best to reserve it within the range it belongs to.
	// but it really doesn't matter, we may just reserve it in all incrementers,
	// that is much simpler and works anyway
	for _, inc := range g.ranges {
		inc.Reserve(octets)
	}

	return nil
}

func (g *TUNIPGenerator) Next() (net.IP, error) {
	g.mx.Lock()
	defer g.mx.Unlock()

	for i := g.currentRange + 1; i != g.currentRange; i++ {
		if i >= len(g.ranges) {
			i = 0
		}

		ip, err := g.ranges[i].Next()
		if err != nil {
			continue
		}

		return ip, nil
	}

	return nil, errors.New("no free IPs left")
}

func fetchIPv4Bytes(ip net.IP) ([4]uint8, error) {
	if len(ip) == net.IPv4len {
		return [4]uint8{ip[0], ip[1], ip[2], ip[3]}, nil
	}

	if len(ip) == net.IPv6len &&
		isZeros(ip[0:10]) &&
		ip[10] == 0xff &&
		ip[11] == 0xff {
		return [4]uint8{ip[12], ip[13], ip[14], ip[15]}, nil
	}

	return [4]uint8{}, errors.New("address is not of v4")
}

func isZeros(p net.IP) bool {
	for i := 0; i < len(p); i++ {
		if p[i] != 0 {
			return false
		}
	}
	return true
}
