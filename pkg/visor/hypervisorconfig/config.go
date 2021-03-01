package hypervisorconfig

import (
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/skycoin/dmsg/cipher"

	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/util/pathutil"
)

//go:generate readmegen -n Config -o ./README.md ./config.go

const (
	defaultHTTPAddr         = ":8000"
	defaultCookieExpiration = 12 * time.Hour
	hashKeyLen              = 64
	blockKeyLen             = 32
)

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

// Config configures the hypervisor.
type Config struct {
	UIAssets      fs.FS         `json:"-"`
	PK            cipher.PubKey `json:"-"`
	SK            cipher.SecKey `json:"-"`
	DBPath        string        `json:"db_path"`             // Path to store database file.
	EnableAuth    bool          `json:"enable_auth"`         // Whether to enable user management.
	Cookies       CookieConfig  `json:"cookies"`             // Configures cookies (for session management).
	DmsgDiscovery string        `json:"-"`                   // Dmsg discovery address.
	DmsgPort      uint16        `json:"dmsg_port,omitempty"` // Dmsg port to serve on.
	HTTPAddr      string        `json:"http_addr"`           // HTTP address to serve API/web UI on.
	EnableTLS     bool          `json:"enable_tls"`          // Whether to enable TLS.
	TLSCertFile   string        `json:"tls_cert_file"`       // TLS cert file location.
	TLSKeyFile    string        `json:"tls_key_file"`        // TLS key file location.
}

// MakeConfig returns hypervisor config.
func MakeConfig(testenv bool) Config {
	var c Config
	c.FillDefaults(testenv)
	return c
}

// GenerateWorkDirConfig generates a config with default values and uses db from current working directory.
func GenerateWorkDirConfig(testenv bool) Config {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalf("failed to generate WD config: %s", dir)
	}
	c := MakeConfig(testenv)
	c.DBPath = filepath.Join(dir, "users.db")
	return c
}

// GenerateHomeConfig generates a config with default values and uses db from user's home folder.
func GenerateHomeConfig(testenv bool) Config {
	c := MakeConfig(testenv)
	c.DBPath = filepath.Join(pathutil.HomeDir(), skyenv.DefaultHypervisorDB)
	return c
}

// GenerateLocalConfig generates a config with default values and uses db from shared folder.
func GenerateLocalConfig(testenv bool) Config {
	c := MakeConfig(testenv)
	c.DBPath = "/usr/local/skycoin/hypervisor/users.db"
	return c
}

// FillDefaults fills the config with default values.
func (c *Config) FillDefaults(testEnv bool) {
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
		if testEnv {
			c.DmsgDiscovery = skyenv.TestDmsgDiscAddr
		} else {
			c.DmsgDiscovery = skyenv.DefaultDmsgDiscAddr
		}
	}

	if c.DmsgPort == 0 {
		c.DmsgPort = skyenv.DmsgHypervisorPort
	}

	if c.HTTPAddr == "" {
		c.HTTPAddr = defaultHTTPAddr
	}

	c.Cookies.FillDefaults()

	c.EnableAuth = skyenv.DefaultEnableAuth

	c.EnableTLS = skyenv.DefaultEnableTLS

	c.TLSCertFile = skyenv.DefaultTLSCert
	c.TLSKeyFile = skyenv.DefaultTLSKey

}

// Parse parses the file in path, and decodes to the config.
func (c *Config) Parse(path string) error {
	var err error
	if path, err = filepath.Abs(path); err != nil {
		return err
	}

	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}

	defer func() {
		if err := f.Close(); err != nil {
			log.Fatalf("Failed to close file %s: %v", f.Name(), err)
		}
	}()

	return json.NewDecoder(f).Decode(c)
}

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
	c.ExpiresDuration = defaultCookieExpiration
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
func (c *CookieConfig) SameSite() http.SameSite {
	// using default value for now
	return http.SameSiteDefaultMode
}
