package netutil

import "net"

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

func HasPublicIP() (bool, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false, err
	}

	for _, i := range ifaces {
		addrs, err := i.Addrs()
		if err != nil {
			return false, err
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if isPublicIP(ip) {
				return true, nil
			}
		}
	}

	return false, nil
}

func isPublicIP(IP net.IP) bool {
	if IP.IsLoopback() || IP.IsLinkLocalMulticast() || IP.IsLinkLocalUnicast() {
		return false
	}

	if ip4 := IP.To4(); ip4 != nil {
		switch {
		case ip4[0] == 10:
			return false
		case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
			return false
		case ip4[0] == 192 && ip4[1] == 168:
			return false
		default:
			return true
		}
	}

	return false
}
