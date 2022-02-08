package netutil

import (
	"io"
	"net"
	"net/http"
)

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

// LocalProtocol check a condition to use dmsghttp or direct url
func LocalProtocol() bool {
	resp, err := http.Get("https://ipinfo.io/country")
	if err != nil {
		return false
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}
	if string(respBody)[:2] == "CN" {
		return true
	}
	return false
}
