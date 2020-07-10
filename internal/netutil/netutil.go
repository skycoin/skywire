package netutil

import "net"

// LocalAddresses returns a list of all local addresses
func LocalAddresses() ([]string, error) {
	result := make([]string, 0)

	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}

	for _, addr := range addresses {
		switch v := addr.(type) {
		case *net.IPNet:
			if v.IP.IsGlobalUnicast() || v.IP.IsLoopback() {
				result = append(result, v.IP.String())
			}
		case *net.IPAddr:
			if v.IP.IsGlobalUnicast() || v.IP.IsLoopback() {
				result = append(result, v.IP.String())
			}
		}
	}

	return result, nil
}
