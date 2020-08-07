package visorconfig

import (
	"github.com/skycoin/dmsg/cipher"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/snet"
)

// V0Name is the version string before proper versioning is implemented.
const (
	V0Name          = "v0.0.0"
	V0NameOldFormat = "1.0"
)

// V0 is visor config v0.0.0
type V0 struct {
	KeyPair struct {
		PubKey cipher.PubKey `json:"public_key"`
		SecKey cipher.SecKey `json:"secret_key"`
	} `json:"key_pair"`

	Dmsg *snet.DmsgConfig `json:"dmsg"`

	DmsgPty *V1Dmsgpty `json:"dmsg_pty,omitempty"`

	STCP *snet.STCPConfig `json:"stcp,omitempty"`

	Transport *struct {
		Discovery string      `json:"discovery"`
		LogStore  *V1LogStore `json:"log_store"`
	} `json:"transport"`

	Routing *V1Routing `json:"routing"`

	UptimeTracker *V1UptimeTracker `json:"uptime_tracker,omitempty"`

	Apps []struct {
		App       string       `json:"app"`
		AutoStart bool         `json:"auto_start"`
		Port      routing.Port `json:"port"`
		Args      []string     `json:"args,omitempty"`
	} `json:"apps"`

	TrustedVisors []cipher.PubKey `json:"trusted_visors"`
	Hypervisors   []struct {
		PubKey cipher.PubKey `json:"public_key"`
	} `json:"hypervisors"`

	AppsPath  string `json:"apps_path"`
	LocalPath string `json:"local_path"`

	LogLevel        string   `json:"log_level"`
	ShutdownTimeout Duration `json:"shutdown_timeout,omitempty"` // time value, examples: 10s, 1m, etc

	Interfaces *struct {
		RPCAddress string `json:"rpc"` // RPC address and port for command-line interface (leave blank to disable RPC interface).
	} `json:"interfaces"`

	AppServerAddr string `json:"app_server_addr"`

	RestartCheckDelay string `json:"restart_check_delay,omitempty"`
}
