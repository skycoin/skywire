package config

import (
	"bytes"
	"encoding/base64"
	"log"
	"text/template"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// VisorConfig defines  skywire-visor configuration
type VisorConfig struct {
	Name   string          `json:"name"`
	Config *visorconfig.V1 `json:"config"`
	Cmd    string          `json:"cmd"`
}

// AddVisorExplicitly adds an explicit skywire node configuration to environment configuration
func (env *EnvConfig) AddVisorExplicitly(visor VisorConfig) *EnvConfig {
	env.Visors = append(env.Visors, visor)
	return env
}

func _cmd(tmplt string, args interface{}) string {
	t, err := template.New("Cmd").Parse(tmplt)
	if err != nil {
		log.Fatalf("error %v parsing template: %v\n", err, tmplt)
	}

	buf := bytes.Buffer{}
	if err := t.Execute(&buf, args); err != nil {
		log.Fatalf("error %v executing args %v in template %v", err, args, tmplt)
	}

	return buf.String()
}

// AddVisor adds a skywire node configuration
func (env *EnvConfig) AddVisor(visorName string, apps []appserver.AppConfig, rpcAddress string) *EnvConfig {
	visorCfg := DefaultPublicVisorConfig()

	visorCfg.Dmsg.Discovery = env.Skywire.DmsgDiscovery.Address
	visorCfg.Transport.Discovery = env.Skywire.TransportDiscovery.Address
	visorCfg.Routing.RouteFinder = env.Skywire.RouteFinder.Address
	visorCfg.Routing.RouteSetupNodes = []cipher.PubKey{env.Skywire.SetupNode.PubKey}
	visorCfg.Launcher = &visorconfig.Launcher{
		Apps: apps,
	}
	visorCfg.CLIAddr = rpcAddress

	env.AddVisorExplicitly(VisorConfig{
		Name:   visorName,
		Config: visorCfg,
		Cmd:    _cmd(env.Runners.SkywireVisor, struct{ Name string }{visorName}),
	})

	return env
}

// DefaultPublicVisorConfig constructs skywire-visor config with global skywire-services
func DefaultPublicVisorConfig() *visorconfig.V1 {
	conf := visorconfig.V1{
		Common: &visorconfig.Common{},
	}

	conf.PK, conf.SK = cipher.GenerateKeyPair()

	conf.Dmsg = &dmsgc.DmsgConfig{
		Discovery:     PublicDmsgDiscovery,
		SessionsCount: 1,
	}

	passcode := base64.StdEncoding.EncodeToString(cipher.RandByte(8))
	conf.Launcher = &visorconfig.Launcher{
		Apps: []appserver.AppConfig{
			{Name: "skychat", Port: 1, AutoStart: true, Args: []string{}},
			{Name: "skysocks", Port: 3, AutoStart: true, Args: []string{"-passcode", passcode}},
		},
		BinPath: "./apps",
	}

	conf.Transport = &visorconfig.Transport{
		Discovery:         PublicDmsgDiscovery,
		PublicAutoconnect: false,
	}

	sPK := cipher.PubKey{}
	if err := sPK.UnmarshalText([]byte(PublicSetupNode)); err != nil {
		log.Printf("Failed to unmarshal global setup node public key %q: %s", PublicSetupNode, err)
	}

	conf.Routing = &visorconfig.Routing{
		RouteSetupNodes: []cipher.PubKey{sPK},
		RouteFinder:     PublicRouteFinder,
	}

	conf.Hypervisors = make([]cipher.PubKey, 0)
	conf.LogLevel = "info"
	conf.LocalPath = "./local"
	conf.CLIAddr = "localhost:3435"
	conf.IsPublic = false
	return &conf
}

func (visorConfig VisorConfig) String() string {
	return PrintJSON(visorConfig)
}
