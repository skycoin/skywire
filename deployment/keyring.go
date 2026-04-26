// Package deployment keyring.go
//
// Keyring is a single JSON file that holds all public/secret key pairs
// for a skywire deployment — services, dmsg servers, route setup nodes,
// transport setup nodes, survey whitelist, network monitors, and the
// reward system.
//
// The file is NOT committed to the repo. It lives on the operator's
// machine and is used to:
//   - Derive individual keyfiles for each service
//   - Populate compose.yaml env vars
//   - Generate the public-key-only deployment config (services-config.json)
//
// Generate a template:
//
//	skywire svc keyring template > keyring.json
//
// Generate all keyfiles from the keyring:
//
//	skywire svc keyring export --dir ./keys
//
// Print only public keys (safe to share):
//
//	skywire svc keyring pks
package deployment

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/skycoin/skywire/pkg/cipher"
)

// KeyPair holds a public and secret key for a deployment identity.
type KeyPair struct {
	PK cipher.PubKey `json:"pk"`
	SK cipher.SecKey `json:"sk"`
}

// Keyring holds all key pairs for a skywire deployment.
type Keyring struct {
	// Services
	DmsgDiscovery      *KeyPair `json:"dmsg_discovery,omitempty"`
	TransportDiscovery *KeyPair `json:"transport_discovery,omitempty"`
	AddressResolver    *KeyPair `json:"address_resolver,omitempty"`
	RouteFinder        *KeyPair `json:"route_finder,omitempty"`
	ServiceDiscovery   *KeyPair `json:"service_discovery,omitempty"`
	UptimeTracker      *KeyPair `json:"uptime_tracker,omitempty"`
	SetupNode          *KeyPair `json:"setup_node,omitempty"`
	ConfService        *KeyPair `json:"conf_service,omitempty"`
	NetworkMonitor     *KeyPair `json:"network_monitor,omitempty"`

	// DMSG servers (multiple)
	DmsgServers []NamedKeyPair `json:"dmsg_servers,omitempty"`

	// Route setup nodes (multiple — includes embedded RSN keys)
	RouteSetupNodes []NamedKeyPair `json:"route_setup_nodes,omitempty"`

	// Transport setup nodes (multiple — includes embedded TPS keys)
	TransportSetupNodes []NamedKeyPair `json:"transport_setup_nodes,omitempty"`

	// Survey whitelist
	SurveyWhitelist []NamedKeyPair `json:"survey_whitelist,omitempty"`

	// Network monitor keys (multiple — for deregistration authorization)
	NetworkMonitorKeys []NamedKeyPair `json:"network_monitor_keys,omitempty"`

	// Reward system
	RewardSystem *KeyPair `json:"reward_system,omitempty"`

	// Visor (the deployment's own visor, if any)
	Visor *KeyPair `json:"visor,omitempty"`
}

// NamedKeyPair is a KeyPair with a human-readable name.
type NamedKeyPair struct {
	Name string        `json:"name"`
	PK   cipher.PubKey `json:"pk"`
	SK   cipher.SecKey `json:"sk"`
}

// LoadKeyring reads a keyring from a JSON file.
func LoadKeyring(path string) (*Keyring, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read keyring: %w", err)
	}
	var kr Keyring
	if err := json.Unmarshal(data, &kr); err != nil {
		return nil, fmt.Errorf("parse keyring: %w", err)
	}
	return &kr, nil
}

// SaveKeyring writes a keyring to a JSON file with restricted permissions.
func SaveKeyring(path string, kr *Keyring) error {
	data, err := json.MarshalIndent(kr, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal keyring: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write keyring: %w", err)
	}
	return nil
}

// ExportKeyfiles writes each key pair to a separate file in the given
// directory. The filename is the identity name (e.g., "dmsg_discovery.key").
func (kr *Keyring) ExportKeyfiles(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create key dir: %w", err)
	}

	writeKey := func(name string, sk cipher.SecKey) error {
		path := dir + "/" + name + ".key"
		return os.WriteFile(path, []byte(sk.Hex()+"\n"), 0600) //nolint:gosec
	}

	pairs := map[string]*KeyPair{
		"dmsg_discovery":      kr.DmsgDiscovery,
		"transport_discovery": kr.TransportDiscovery,
		"address_resolver":    kr.AddressResolver,
		"route_finder":        kr.RouteFinder,
		"service_discovery":   kr.ServiceDiscovery,
		"uptime_tracker":      kr.UptimeTracker,
		"setup_node":          kr.SetupNode,
		"conf_service":        kr.ConfService,
		"network_monitor":     kr.NetworkMonitor,
		"reward_system":       kr.RewardSystem,
		"visor":               kr.Visor,
	}
	for name, kp := range pairs {
		if kp != nil && !kp.SK.Null() {
			if err := writeKey(name, kp.SK); err != nil {
				return err
			}
		}
	}
	for _, nkp := range kr.DmsgServers {
		if !nkp.SK.Null() {
			if err := writeKey("dmsg_server_"+nkp.Name, nkp.SK); err != nil {
				return err
			}
		}
	}
	for _, nkp := range kr.RouteSetupNodes {
		if !nkp.SK.Null() {
			if err := writeKey("route_setup_"+nkp.Name, nkp.SK); err != nil {
				return err
			}
		}
	}
	for _, nkp := range kr.TransportSetupNodes {
		if !nkp.SK.Null() {
			if err := writeKey("transport_setup_"+nkp.Name, nkp.SK); err != nil {
				return err
			}
		}
	}
	for _, nkp := range kr.NetworkMonitorKeys {
		if !nkp.SK.Null() {
			if err := writeKey("nm_"+nkp.Name, nkp.SK); err != nil {
				return err
			}
		}
	}
	for _, nkp := range kr.SurveyWhitelist {
		if !nkp.SK.Null() {
			if err := writeKey("survey_wl_"+nkp.Name, nkp.SK); err != nil {
				return err
			}
		}
	}
	return nil
}

// PublicKeys returns a summary of all public keys (safe to share/print).
func (kr *Keyring) PublicKeys() map[string]string {
	pks := make(map[string]string)

	addPK := func(name string, kp *KeyPair) {
		if kp != nil && !kp.PK.Null() {
			pks[name] = kp.PK.Hex()
		}
	}

	addPK("dmsg_discovery", kr.DmsgDiscovery)
	addPK("transport_discovery", kr.TransportDiscovery)
	addPK("address_resolver", kr.AddressResolver)
	addPK("route_finder", kr.RouteFinder)
	addPK("service_discovery", kr.ServiceDiscovery)
	addPK("uptime_tracker", kr.UptimeTracker)
	addPK("setup_node", kr.SetupNode)
	addPK("conf_service", kr.ConfService)
	addPK("network_monitor", kr.NetworkMonitor)
	addPK("reward_system", kr.RewardSystem)
	addPK("visor", kr.Visor)

	for _, nkp := range kr.DmsgServers {
		pks["dmsg_server_"+nkp.Name] = nkp.PK.Hex()
	}
	for _, nkp := range kr.RouteSetupNodes {
		pks["route_setup_"+nkp.Name] = nkp.PK.Hex()
	}
	for _, nkp := range kr.TransportSetupNodes {
		pks["transport_setup_"+nkp.Name] = nkp.PK.Hex()
	}
	for _, nkp := range kr.NetworkMonitorKeys {
		pks["nm_"+nkp.Name] = nkp.PK.Hex()
	}
	for _, nkp := range kr.SurveyWhitelist {
		pks["survey_wl_"+nkp.Name] = nkp.PK.Hex()
	}

	return pks
}
