// Package visorconfig pkg/visor/visorconfig/hypergo
//
// The CookieConfig.SameSite() method (which returns http.SameSite)
// is the only net/http user in this file; it's moved to
// hypervisorconfig_native.go under //go:build !js so this file
// compiles cleanly for js/wasm. See services.go's package doc for
// the broader rationale.
package visorconfig

import (
	"encoding/hex"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skyenv"
)

const (
	httpAddr         = ":8000"
	cookieExpiration = 12 * time.Hour
	hashKeyLen       = 64
	blockKeyLen      = 32
)

// HTTPAddr returns the httpAddr
func HTTPAddr() string {
	return httpAddr
}

// Key allows a byte slice to be marshaled or unmarshaled from a hex string.
type Key []byte

// String implements fmt.Stringer
func (hk Key) String() string {
	return hex.EncodeToString(hk)
}

// MarshalText implements encoding.TextMarshaler
func (hk Key) MarshalText() ([]byte, error) {
	return []byte(hk.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler
func (hk *Key) UnmarshalText(text []byte) error {
	*hk = make([]byte, hex.DecodedLen(len(text)))
	_, err := hex.Decode(*hk, text)

	return err
}

// HypervisorConfig configures the hypervisor.
type HypervisorConfig struct {
	Enable        bool               `json:"enable"` // Whether the hypervisor is enabled (starts HTTP + DMSG on visor startup).
	UIAssets      fs.FS              `json:"-"`
	PK            cipher.PubKey      `json:"-"`
	SK            cipher.SecKey      `json:"-"`
	DBPath        string             `json:"db_path"`                   // Path to store database file.
	EnableAuth    bool               `json:"enable_auth"`               // Whether to enable user management.
	Cookies       CookieConfig       `json:"cookies"`                   // Configures cookies (for session management).
	DmsgDiscovery string             `json:"-"`                         // Dmsg discovery address.
	DmsgPort      uint16             `json:"dmsg_port,omitempty"`       // Dmsg port to serve on.
	HTTPAddr      string             `json:"http_addr"`                 // HTTP address to serve API/web UI on.
	EnableTLS     bool               `json:"enable_tls"`                // Whether to enable TLS.
	TLSCertFile   string             `json:"tls_cert_file"`             // TLS cert file location.
	TLSKeyFile    string             `json:"tls_key_file"`              // TLS key file location.
	TPViz         TPVizConfig        `json:"tp_viz"`                    // Transport visualizer config.
	LANDmsgServer *LANDmsgServerConf `json:"lan_dmsg_server,omitempty"` // LAN DMSG server config.
	// CXOSubscribeInterval is the resync floor for the on-demand
	// CXO subscription manager. Subscriptions stay open while a UI
	// tab is acquired; the manager won't tear down a feed sooner
	// than this after its last release. 0 falls back to the
	// package default (5m). Common knob to tune the bandwidth /
	// freshness tradeoff for hvui tabs that source CXO data.
	CXOSubscribeInterval Duration `json:"cxo_subscribe_interval,omitempty"`
}

// DefaultHypervisorConfig returns a HypervisorConfig with sensible defaults.
// Used when enabling the hypervisor at runtime on a visor that wasn't configured
// as a hypervisor at startup.
func DefaultHypervisorConfig() HypervisorConfig {
	c := HypervisorConfig{}
	c.FillDefaults(false)
	c.DBPath = filepath.Join(skyenv.SkywirePath, "local", "hypervisor", "users.db")
	return c
}

// LANDmsgServerConf configures an embedded DMSG server for LAN visors,
// optionally reachable from outside the LAN as well.
// When enabled, the hypervisor acts as a local DMSG relay so its managed
// visors don't need to route through public DMSG servers. With Port pinned
// and PublicAddress set, remote visors can also reach the server (operator
// must configure port forwarding on the router for the chosen Port).
// The server keypair is stored in the config so it persists across restarts.
type LANDmsgServerConf struct {
	Enable             bool          `json:"enable"`                         // Enable the LAN/WAN DMSG server.
	Port               int           `json:"port,omitempty"`                 // TCP port (0 = auto-select; auto-selected port changes on every restart, so set explicitly for stable address).
	PublicAddress      string        `json:"public_address,omitempty"`       // Operator-set "host:port" advertised to remote visors (e.g. "203.0.113.5:8082"). Empty = LAN-only.
	MaxSessions        int           `json:"max_sessions,omitempty"`         // Max concurrent sessions (default 100).
	PK                 cipher.PubKey `json:"pk,omitempty"`                   // Server public key (auto-generated if empty).
	SK                 cipher.SecKey `json:"sk,omitempty"`                   // Server secret key (auto-generated if empty).
	PublicDiscoveryURL string        `json:"public_discovery_url,omitempty"` // Override for the dmsg-discovery URL advertised to managed visors (e.g. "http://hypervisor.example.com:8000"). Empty = auto-derived from HTTPAddr when remotely reachable.
}

// TPVizConfig configures the embedded transport visualizer.
type TPVizConfig struct {
	Enable      bool   `json:"enable"`                  // Whether to enable tp viz UI at /tp-viz/.
	SurveyDir   string `json:"survey_dir,omitempty"`    // Directory containing visor surveys.
	CacheMaxAge int    `json:"cache_max_age,omitempty"` // Cache max age in minutes (default: 5).
}

// MakeConfig returns hypervisor config.
func MakeConfig(testenv bool) HypervisorConfig {
	var c HypervisorConfig
	c.FillDefaults(testenv)
	return c
}

// GenerateWorkDirConfig generates a config with default values and uses db from current working directory.
func GenerateWorkDirConfig(testenv bool) HypervisorConfig {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalf("failed to generate WD config: %s", dir)
	}
	c := MakeConfig(testenv)
	c.DBPath = filepath.Join(dir, "users.db")
	return c
}

// FillDefaults fills the config with default values.
func (c *HypervisorConfig) FillDefaults(testEnv bool) {
	if c.PK.Null() || c.SK.Null() {
		c.PK, c.SK = cipher.GenerateKeyPair()
	}

	if len(c.Cookies.HashKey) != hashKeyLen {
		c.Cookies.HashKey = cipher.RandByte(hashKeyLen)
	}

	if len(c.Cookies.BlockKey) != blockKeyLen {
		c.Cookies.BlockKey = cipher.RandByte(blockKeyLen)
	}

	if c.DmsgDiscovery == "" {
		// deployment.Prod / deployment.Test are populated at
		// deployment.init() time from the embedded
		// services-config.json. Use those directly rather than
		// re-unmarshalling — keeps json.Unmarshal off the WASM
		// build graph and avoids duplicating the parse cost on
		// every hypervisor-config FillDefaults call.
		if testEnv {
			c.DmsgDiscovery = deployment.Test.DmsgDiscovery
		} else {
			c.DmsgDiscovery = deployment.Prod.DmsgDiscovery
		}
		if c.DmsgPort == 0 {
			c.DmsgPort = skyenv.DmsgHypervisorPort
		}
		if c.HTTPAddr == "" {
			c.HTTPAddr = httpAddr
		}
		c.Cookies.FillDefaults()
		c.TPViz.Enable = true
		c.EnableAuth = skyenv.EnableAuth
		c.EnableTLS = skyenv.EnableTLS
		c.TLSCertFile = skyenv.TLSCert
		c.TLSKeyFile = skyenv.TLSKey

	}
}

// Parse parses the file at path and decodes its JSON into c.
//
// Implementation lives in hypervisorconfig_native.go under
// //go:build !js — see services.go's package doc for the rationale
// on keeping encoding/json out of the WASM build graph. Browsers
// have no filesystem so this method is genuinely unavailable in
// that context; callers under js/wasm should construct
// HypervisorConfig in memory.

// CookieConfig configures cookies used for hypervisor.
type CookieConfig struct {
	HashKey  Key `json:"hash_key"`  // Signs the cookie: 32 or 64 bytes.
	BlockKey Key `json:"block_key"` // Encrypts the cookie: 16 (AES-128), 24 (AES-192), 32 (AES-256) bytes. (optional)

	ExpiresDuration time.Duration `json:"expires_duration"` // Used for determining the 'expires' value for cookies.

	Path   string `json:"path"`   // optional
	Domain string `json:"domain"` // optional

	TLS bool `json:"-"`
}

// FillDefaults fills config with default values.
func (c *CookieConfig) FillDefaults() {
	c.ExpiresDuration = cookieExpiration
	c.Path = "/"

	c.TLS = false
}

// Secure gets cookie's `Secure` value.
func (c *CookieConfig) Secure() bool {
	return c.TLS
}

// HTTPOnly gets cookie's `HTTPOnly` value.
func (c *CookieConfig) HTTPOnly() bool {
	return !c.TLS
}

// SameSite gets cookie's `SameSite` value.
//
// Implementation lives in hypervisorconfig_native.go under
// //go:build !js — see services.go's package doc for the
// rationale on keeping net/http out of the WASM build graph.
