package servicedisc

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/skycoin/dmsg/cipher"
)

const (
	// ServiceTypeProxy stands for the proxy discovery.
	ServiceTypeProxy = "proxy"
	// ServiceTypeVPN stands for the VPN discovery.
	ServiceTypeVPN = "vpn"
	// ServiceTypeVisor stands for visor.
	ServiceTypeVisor = "visor"
)

// Errors associated with service discovery types.
var (
	ErrInvalidSWAddr = errors.New("invalid skywire address")
)

// SWAddr represents a skywire address.
type SWAddr [len(cipher.PubKey{}) + 2]byte

// NewSWAddr creates a new SWAddr.
func NewSWAddr(pk cipher.PubKey, port uint16) SWAddr {
	var addr SWAddr
	copy(addr[:], pk[:])
	binary.BigEndian.PutUint16(addr[len(addr)-2:], port)
	return addr
}

// PubKey returns the contained public key.
func (a *SWAddr) PubKey() (pk cipher.PubKey) {
	copy(pk[:], a[:])
	return
}

// Port returns the contained port.
func (a *SWAddr) Port() uint16 {
	return binary.BigEndian.Uint16(a[len(a)-2:])
}

// String implements io.Stringer
func (a *SWAddr) String() string {
	return a.PubKey().String() + ":" + strconv.FormatUint(uint64(a.Port()), 10)
}

// MarshalText implements encoding.TextMarshaler
func (a *SWAddr) MarshalText() ([]byte, error) {
	return []byte(a.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaller
func (a *SWAddr) UnmarshalText(text []byte) error {
	parts := bytes.SplitN(text, []byte{':'}, 2)
	switch len(parts) {
	case 0:
		return ErrInvalidSWAddr
	case 1:
		parts = append(parts, []byte("0"))
	}
	var pk cipher.PubKey
	if err := pk.UnmarshalText(parts[0]); err != nil {
		return err
	}
	copy(a[:], pk[:])
	port, err := strconv.ParseUint(string(parts[1]), 10, 16)
	if err != nil {
		return err
	}
	binary.BigEndian.PutUint16(a[len(a)-2:], uint16(port))
	return nil
}

// MarshalBinary implements encoding.BinaryMarshaller
func (a *SWAddr) MarshalBinary() ([]byte, error) {
	return a[:], nil
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (a *SWAddr) UnmarshalBinary(data []byte) error {
	copy(a[:], data)
	return nil
}

// GeoLocation represents a geolocation point.
type GeoLocation struct {
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Country string  `json:"country,omitempty"`
	Region  string  `json:"region,omitempty"`
}

// Stats provides various statistics on the service-discovery service.
type Stats struct {
	ConnectedClients int `json:"connected_clients"`
}

// Service represents a service entry in service-discovery.
type Service struct {
	Addr     SWAddr       `json:"address"`
	Type     string       `json:"type"`
	Stats    *Stats       `json:"stats,omitempty"`
	Geo      *GeoLocation `json:"geo,omitempty"`
	Version  string       `json:"version,omitempty"`
	LocalIPs []string     `json:"local_ips,omitempty"`
}

// MarshalBinary implements encoding.BinaryMarshaller
func (p *Service) MarshalBinary() ([]byte, error) {
	return json.Marshal(p)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler
func (p *Service) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, p)
}

// Check ensures fields are valid.
func (p Service) Check() error {
	if p.Addr.PubKey().Null() {
		return errors.New("public key cannot be null in address")
	}
	if p.Addr.Port() == 0 {
		return errors.New("port cannot be 0 in address")
	}
	return nil
}
