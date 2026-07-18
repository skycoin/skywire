// Package servicedisc pkg/servicedisc/types.go c2-net-discovery
package servicedisc

import (
	"bytes"
	"database/sql/driver"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/geo"
)

const (
	// ServiceTypeSkysocks stands for the skysocks discovery.
	ServiceTypeSkysocks = "skysocks"
	// ServiceTypeProxy stands for the proxy discovery. Proxy and Skysocks are same
	ServiceTypeProxy = "proxy"
	// ServiceTypeVPN stands for the VPN discovery.
	ServiceTypeVPN = "vpn"
	// ServiceTypeVisor stands for visor.
	ServiceTypeVisor = "visor"
	// ServiceTypeCoin stands for a discoverable fibercoin node. Intentionally
	// GENERIC — any fibercoin, not skycoin specifically — so a (browser/wasm)
	// thin-client wallet can discover a node for the right coin/network over
	// dmsg and reach its HTTP API through the mesh, with no local full node and
	// no launch flags. The coin identity + capabilities live in Service.Coin
	// (CoinInfo); the node's dmsg address is Service.Addr.
	ServiceTypeCoin = "coin"
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
	LocalIPs      stringArray       `json:"local_ips,omitempty" gorm:"type:text[]"`
	Info          *VPNInfo          `json:"info,omitempty" gorm:"-"`
	// Coin, set for ServiceTypeCoin entries, carries the fibercoin identity +
	// capabilities so a thin-client wallet can filter discovery by coin/network
	// without launch flags. Transient (gorm:"-"), like Info.
	Coin *CoinInfo `json:"coin,omitempty" gorm:"-"`
}

// CoinInfo describes a discoverable fibercoin node (ServiceTypeCoin). The
// enclosing Service.Addr is the node's dmsg address (the forwarding visor's PK +
// dmsg port). Every field here is DETECTED from the node's own health/metadata
// response — never operator-configured — so an advertisement can't be
// misconfigured (accidentally or maliciously) to claim the wrong chain. A wallet
// filters and TRUSTS on the cryptographic chain identity (BlockchainPubKey /
// genesis), not on the human name. Generic across fibercoins.
type CoinInfo struct {
	// BlockchainPubKey is the block-publisher public key that signs this chain —
	// the AUTHORITATIVE, cryptographic chain identifier. A wallet must verify it
	// is on the intended chain by THIS key, not by the human name, so a node can
	// never spoof another chain by calling itself "skycoin".
	BlockchainPubKey string `json:"blockchain_pubkey,omitempty"`
	// GenesisAddress + GenesisSignature anchor the chain's genesis block — an
	// independent identity check beyond the block-publisher key.
	GenesisAddress   string `json:"genesis_address,omitempty"`
	GenesisSignature string `json:"genesis_signature,omitempty"`
	// Fiber is the human-readable coin name (e.g. "skycoin", "mdl") for display +
	// coarse filtering ONLY — never the trust anchor; BlockchainPubKey is.
	Fiber string `json:"fiber,omitempty"`
	// Network is the coin network — "main" or "test".
	Network string `json:"network,omitempty"`
	// Version + Commit identify the DAEMON's OWN build, as reported by its HTTP
	// API (skycoin /api/v1/health -> version{version, commit}). This is
	// deliberately NOT the visor's compiled-in skycoin import (`skywire -d`): the
	// daemon runs independently, so its build can differ from the visor's, and the
	// advertisement must reflect the ACTUAL running node. Commit pins the exact
	// build precisely.
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
	// HeadSeq is the current block-head sequence, so a wallet can prefer a
	// well-synced node. Also a freshness signal alongside the SD re-registration.
	HeadSeq uint64 `json:"head_seq,omitempty"`
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
