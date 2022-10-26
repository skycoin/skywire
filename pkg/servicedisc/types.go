// Package servicedisc pkg/servicedisc/types.go
package servicedisc

import (
	"bytes"
	"database/sql/driver"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	pq "github.com/lib/pq"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/geo"
)

const (
	// ServiceTypeSkysocks stands for the skysocks discovery.
	ServiceTypeSkysocks = "skysocks"
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

// Scan implement a scanner to get data from database
func (a *SWAddr) Scan(value interface{}) error {
	str, ok := value.(string)
	if !ok {
		return errors.New("provided value not of type string")
	}

	err := a.UnmarshalText([]byte(str))
	if err != nil {
		return err
	}

	return nil
}

// Value is a method to get value of data fetched from database
func (a SWAddr) Value() (driver.Value, error) {
	str := a.String()
	return str, nil
}

// Service represents a service entry in service-discovery.
type Service struct {
	ID            uint              `json:"-" gorm:"primarykey"`
	CreatedAt     time.Time         `json:"-"`
	Addr          SWAddr            `json:"address"`
	Type          string            `json:"type"`
	Geo           *geo.LocationData `json:"geo,omitempty" gorm:"embedded"`
	DisplayNodeIP bool              `json:"display_node_ip,omitempty"`
	Version       string            `json:"version,omitempty"`
	LocalIPs      pq.StringArray    `json:"local_ips,omitempty" gorm:"type:text[]"`
	Info          *VPNInfo          `json:"info,omitempty" gorm:"-"`
}

// VPNInfo used for showing VPN metrics info, like latency, uptime and count of connections
type VPNInfo struct {
	Latency     float64 `json:"latency,omitempty"`
	Uptime      float64 `json:"uptime,omitempty"`
	Connections int     `json:"connections,omitempty"`
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

func (p Service) String() string {
	var serviceMap map[string]interface{}

	data, _ := json.Marshal(p)            // nolint:errcheck
	_ = json.Unmarshal(data, &serviceMap) // nolint:errcheck

	serviceMap["address"] = p.Addr.String()

	sString, _ := json.Marshal(serviceMap) // nolint:errcheck
	return string(sString)
}
