// Package config pkg/serviceconfig/env_scripts.go c4-net-discovery
package config

// EnvScripts is a collection of environment scripts
type EnvScripts struct {
	Startup  string `json:"startup,omitempty"`
	TearDown string `json:"teardown,omitempty"`
	EnvVars  string `json:"env_vars,omitempty"`
}

func (scr EnvScripts) String() string {
	return PrintJSON(scr)
}
