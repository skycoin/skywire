// Package vpn internal/vpn/subnet_ip_incrementer.go
package vpn

import (
	"errors"
	"net"
	"sync"
)

// subnetIPIncrementer is used to increment over the subnet IP address
// between the specified borders.
type subnetIPIncrementer struct {
	mx                sync.Mutex
	octets            [4]uint8
	octetLowerBorders [4]uint8
	octetBorders      [4]uint8
	step              uint8
	reserved          map[[4]uint8]struct{}
}

func newSubnetIPIncrementer(octetLowerBorders, octetBorders [4]uint8, step uint8) *subnetIPIncrementer {
	return &subnetIPIncrementer{
		mx:                sync.Mutex{},
		octets:            octetLowerBorders,
		octetLowerBorders: octetLowerBorders,
		octetBorders:      octetBorders,
		step:              step,
		reserved:          make(map[[4]uint8]struct{}),
	}
}

func (inc *subnetIPIncrementer) next() (net.IP, error) {
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

					var isReserved bool
					// need to check all of the IPs within the generated subnet.
					// since we're excluding some of the IPs from generation, these
					// may be within some of the generated ranges.
					for i := o4; i < o4+inc.step; i++ {
						generatedIP[3] = i

						if _, ok := inc.reserved[generatedIP]; ok {
							isReserved = true
							break
						}
					}

					if !isReserved {
						generatedIP[3] = o4
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

func (inc *subnetIPIncrementer) reserve(octets [4]uint8) {
	inc.mx.Lock()
	defer inc.mx.Unlock()

	inc.reserved[octets] = struct{}{}
}
