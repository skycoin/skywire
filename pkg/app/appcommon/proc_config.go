package appcommon

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/google/uuid"

	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

const (
	// EnvProcConfig is the env name which contains a JSON-encoded proc config.
	EnvProcConfig = "PROC_CONFIG"
)

var (
	// ErrProcConfigEnvNotDefined occurs when an expected env is not defined.
	ErrProcConfigEnvNotDefined = fmt.Errorf("env '%s' is not defined", EnvProcConfig)
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
	AppName     string        `json:"app_name"`
	AppSrvAddr  string        `json:"app_server_addr"`
	ProcKey     ProcKey       `json:"proc_key"`
	ProcArgs    []string      `json:"proc_args"`
	ProcEnvs    []string      `json:"proc_envs"` // Additional env variables. Will be overwritten if they conflict with skywire-app specific envs.
	ProcWorkDir string        `json:"proc_work_dir"`
	VisorPK     cipher.PubKey `json:"visor_pk"`
	RoutingPort routing.Port  `json:"routing_port"`
	BinaryLoc   string        `json:"binary_loc"`
	LogDBLoc    string        `json:"log_db_loc"`
}

// ProcConfigFromEnv obtains a ProcConfig from the associated env variable, returning an error if any.
func ProcConfigFromEnv() (ProcConfig, error) {
	v, ok := os.LookupEnv(EnvProcConfig)
	if !ok {
		return ProcConfig{}, ErrProcConfigEnvNotDefined
	}
	var conf ProcConfig
	if err := json.Unmarshal([]byte(v), &conf); err != nil {
		return ProcConfig{}, fmt.Errorf("invalid %s env value: %w", EnvProcConfig, err)
	}
	return conf, nil
}

// EnsureKey ensures that a proc key is provided in the ProcConfig.
func (c *ProcConfig) EnsureKey() {
	if c.ProcKey.Null() {
		c.ProcKey = RandProcKey()
	}
}

// Envs returns the env variables that are passed to the associated proc.
func (c *ProcConfig) Envs() []string {
	const format = "%s=%s"
	return append(c.ProcEnvs, fmt.Sprintf(format, EnvProcConfig, string(c.encodeJSON())))
}

func (c *ProcConfig) encodeJSON() []byte {
	b, err := json.Marshal(c)
	if err != nil {
		panic(err)
	}
	return b
}
