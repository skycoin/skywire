// Package visorconfig pkg/visor/visorconfig/config_compat.go —
// backward-compat JSON unmarshaling for renamed config keys.
//
// V1.Pty (canonical) corresponds to operator-config-json's "pty"
// key. The legacy key "dmsgpty" is still accepted on load so that
// existing operator config.json files don't break across the
// rename. Marshaling always emits the canonical "pty" key — V1's
// struct tag is `json:"pty,omitempty"`.
//
// When both keys appear, the canonical "pty" wins. When neither
// appears, V1.Pty stays nil (the visor's init path treats nil as
// "pty subsystem disabled").
package visorconfig

import (
	"encoding/json"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgspec "github.com/skycoin/skywire/pkg/dmsgc/spec"
	tnspec "github.com/skycoin/skywire/pkg/transport/network/spec"
	tspec "github.com/skycoin/skywire/pkg/transport/spec"
)

// v1JSON mirrors V1's JSON-tagged field set verbatim, MINUS the
// sync.RWMutex. Used as the unmarshal target so V1.UnmarshalJSON
// can decode without copying the mutex (which `go vet` flags as
// a lock-copy violation, and which would be a latent footgun
// even though UnmarshalJSON only runs at config-load time before
// any locking happens).
//
// Must stay in sync with V1's field list. The compat_test pins
// the round-trip so a missing field surfaces immediately.
type v1JSON struct {
	*Common

	Dmsg          *dmsgspec.DmsgConfig `json:"dmsg"`
	Pty           *Pty                 `json:"pty,omitempty"`
	Dmsgscp       *Dmsgscp             `json:"dmsgscp,omitempty"`
	UIServer      *UIServer            `json:"ui_server,omitempty"`
	LogServer     *LogServer           `json:"log_server,omitempty"`
	DmsgWeb       *DmsgWebConfig       `json:"dmsg_web,omitempty"`
	SkynetWeb     *SkynetWebConfig     `json:"skynet_web,omitempty"`
	SkymailBridge *SkymailBridgeConfig `json:"skymail_bridge,omitempty"`
	Rewards       *RewardsConfig       `json:"rewards,omitempty"`
	STCP          *tnspec.STCPConfig   `json:"skywire-tcp,omitempty"`
	Transport     *Transport           `json:"transport"`
	Routing       *Routing             `json:"routing"`
	UptimeTracker *UptimeTracker       `json:"uptime_tracker,omitempty"`
	Launcher      *Launcher            `json:"launcher"`

	Stats   *Stats   `json:"stats,omitempty"`
	Skychat *Skychat `json:"skychat,omitempty"`

	SurveyWhitelist     []cipher.PubKey `json:"survey_whitelist"`
	UserSurveyWhitelist []cipher.PubKey `json:"user_survey_whitelist,omitempty"`
	Hypervisors         []cipher.PubKey `json:"hypervisors"`
	CLIAddr             string          `json:"cli_addr"`

	LogLevel             string                       `json:"log_level"`
	LocalPath            string                       `json:"local_path"`
	StunServers          []string                     `json:"stun_servers"`
	ShutdownTimeout      Duration                     `json:"shutdown_timeout,omitempty"`
	IsPublic             bool                         `json:"is_public"`
	PublicVisorConfig    *PublicVisorConfig           `json:"public_visor,omitempty"`
	GeoIP                string                       `json:"geoip"`
	PersistentTransports []tspec.PersistentTransports `json:"persistent_transports"`
	ConfService          string                       `json:"conf_service,omitempty"`
	ConfServiceDmsg      string                       `json:"conf_service_dmsg,omitempty"`
	SurveyClientSK       string                       `json:"survey_client_sk,omitempty"`

	RewardAddress    string `json:"reward_address,omitempty"`
	RewardSystem     string `json:"reward_system,omitempty"`
	RewardSystemDmsg string `json:"reward_system_dmsg,omitempty"`
	MemoryLimit      string `json:"memory_limit,omitempty"`

	Hypervisor *HypervisorConfig `json:"hypervisor,omitempty"`
}

// UnmarshalJSON implements json.Unmarshaler for V1. Decodes via
// v1JSON (which mirrors V1's field set without the embedded
// sync.RWMutex), copies the result back field-by-field, then
// patches V1.Pty from the legacy "dmsgpty" key if the canonical
// "pty" key was absent.
//
// The mirror struct is seeded with v.Common so caller-populated
// fields like the embedded *Common — set by MakeBaseConfig before
// the JSON decode runs in ReadRaw — survive the unmarshal.
func (v *V1) UnmarshalJSON(data []byte) error {
	mirror := v1JSON{Common: v.Common}
	if err := json.Unmarshal(data, &mirror); err != nil {
		return err
	}

	v.Common = mirror.Common
	v.Dmsg = mirror.Dmsg
	v.Pty = mirror.Pty
	v.Dmsgscp = mirror.Dmsgscp
	v.UIServer = mirror.UIServer
	v.LogServer = mirror.LogServer
	v.DmsgWeb = mirror.DmsgWeb
	v.SkynetWeb = mirror.SkynetWeb
	v.SkymailBridge = mirror.SkymailBridge
	v.Rewards = mirror.Rewards
	v.STCP = mirror.STCP
	v.Transport = mirror.Transport
	v.Routing = mirror.Routing
	v.UptimeTracker = mirror.UptimeTracker
	v.Launcher = mirror.Launcher
	v.Stats = mirror.Stats
	v.Skychat = mirror.Skychat
	v.SurveyWhitelist = mirror.SurveyWhitelist
	v.UserSurveyWhitelist = mirror.UserSurveyWhitelist
	v.Hypervisors = mirror.Hypervisors
	v.CLIAddr = mirror.CLIAddr
	v.LogLevel = mirror.LogLevel
	v.LocalPath = mirror.LocalPath
	v.StunServers = mirror.StunServers
	v.ShutdownTimeout = mirror.ShutdownTimeout
	v.IsPublic = mirror.IsPublic
	v.PublicVisorConfig = mirror.PublicVisorConfig
	v.GeoIP = mirror.GeoIP
	v.PersistentTransports = mirror.PersistentTransports
	v.ConfService = mirror.ConfService
	v.ConfServiceDmsg = mirror.ConfServiceDmsg
	v.SurveyClientSK = mirror.SurveyClientSK
	v.RewardAddress = mirror.RewardAddress
	v.RewardSystem = mirror.RewardSystem
	v.RewardSystemDmsg = mirror.RewardSystemDmsg
	v.MemoryLimit = mirror.MemoryLimit
	v.Hypervisor = mirror.Hypervisor

	if v.Pty == nil {
		var legacy struct {
			Dmsgpty *Pty `json:"dmsgpty"`
		}
		if err := json.Unmarshal(data, &legacy); err == nil && legacy.Dmsgpty != nil {
			v.Pty = legacy.Dmsgpty
		}
	}
	return nil
}
