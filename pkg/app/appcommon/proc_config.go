package appcommon

import (
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

// DefaultAppSrvAddr is the default address to run the app server at.
const DefaultAppSrvAddr = "localhost:5505"

const (
	// EnvProcKey is a name for env arg containing skywire application key.
	EnvProcKey = "PROC_KEY"
	// EnvAppSrvAddr is a name for env arg containing app server address.
	EnvAppSrvAddr = "APP_SERVER_ADDR"
	// EnvVisorPK is a name for env arg containing public key of visor.
	EnvVisorPK = "VISOR_PK"
)

// ProcKey is a unique key to authenticate a proc within the app server.
type ProcKey [16]byte

// RandProcKey generates new proc key.
func RandProcKey() ProcKey {
	return ProcKey(uuid.New())
}

// String implements io.Stringer
func (k ProcKey) String() string {
	return hex.EncodeToString(k[:])
}

// Null returns true if ProcKey is null.
func (k ProcKey) Null() bool {
	return k == (ProcKey{})
}

// MarshalText implements encoding.TextMarshaller
func (k ProcKey) MarshalText() ([]byte, error) {
	return []byte(k.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaller
func (k *ProcKey) UnmarshalText(data []byte) error {
	if len(data) == 0 {
		*k = ProcKey{}
		return nil
	}
	n, err := hex.Decode(k[:], data)
	if err != nil {
		return err
	}
	if n != len(k) {
		return errors.New("invalid proc key length")
	}
	return nil
}

// ProcID identifies the current instance of an app (an app process).
// The visor is responsible for starting apps, and the started process should be provided with a ProcID.
type ProcID uint16

// ProcConfig defines configuration parameters for `Proc`.
type ProcConfig struct {
	AppName     string       `json:"app_name"`
	AppSrvAddr  string       `json:"app_server_addr"`
	ProcKey     ProcKey      `json:"proc_key"`
	ProcArgs    []string     `json:"proc_args"`
	VisorPK     string       `json:"visor_pk"`
	RoutingPort routing.Port `json:"routing_port"`
	BinaryDir   string       `json:"binary_dir"`
	WorkDir     string       `json:"work_dir"`
}

// EnsureKey ensures that a proc key is provided in the ProcConfig.
func (c *ProcConfig) EnsureKey() {
	if c.ProcKey.Null() {
		c.ProcKey = RandProcKey()
	}
}

// BinaryLoc returns the binary path using the associated fields of the ProcConfig.
func (c *ProcConfig) BinaryLoc() string {
	return filepath.Join(c.BinaryDir, c.AppName)
}

// Envs returns the env variables that are passed to the associated proc.
func (c *ProcConfig) Envs() []string {
	const format = "%s=%s"
	return []string{
		fmt.Sprintf(format, EnvProcKey, c.ProcKey),
		fmt.Sprintf(format, EnvAppSrvAddr, c.AppSrvAddr),
		fmt.Sprintf(format, EnvVisorPK, c.VisorPK),
	}
}
