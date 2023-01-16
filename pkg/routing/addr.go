// Package routing pkg/routing/addr.go
package routing

import (
	"errors"
	"fmt"
	"strings"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// Port is a network port number
type Port uint16

const networkType = "skywire"

// Addr represents a network address combining public key and port.
// Implements net.Addr
type Addr struct {
	PubKey cipher.PubKey `json:"pk"`
	Port   Port          `json:"port"`
}

// Network returns type of `a`'s network
func (a Addr) Network() string {
	return networkType
}

func (a Addr) String() string {
	return fmt.Sprintf("%s:%d", a.PubKey, a.Port)
}

// Set implements pflag.Value for Addr.
func (a *Addr) Set(s string) error {
	parts := strings.Split(s, ":")
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
	}
	switch len(parts) {
	case 0:
		a.PubKey = cipher.PubKey{}
		a.Port = 0
		return nil
	case 1:
		return a.PubKey.Set(parts[0])
	case 2:
		if parts[0] == "" {
			a.PubKey = cipher.PubKey{}
		} else {
			if err := a.PubKey.Set(parts[0]); err != nil {
				return err
			}
		}
		if parts[1] == "~" || parts[1] == "" {
			a.Port = 0
		} else {
			_, err := fmt.Sscan(parts[1], &a.Port)
			return err
		}
		return nil
	default:
		return errors.New("invalid dmsg.Addr string")
	}
}
