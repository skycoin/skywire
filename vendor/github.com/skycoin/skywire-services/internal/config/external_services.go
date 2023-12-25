package config

import (
	"encoding/json"
	"log"
)

// DefaultExternalServices returns default externalServices configuration for localhost environments
func (env *EnvConfig) DefaultExternalServices() *EnvConfig {
	env.ExternalServices = ExternalServicesConfig{
		RedisAddress:  "localhost:6379",
		SyslogAddress: "localhost:514",
	}
	return env
}

func (ext ExternalServicesConfig) String() string {
	res, err := json.MarshalIndent(ext, "", "\t")
	if err != nil {
		log.Fatal(err)
	}
	return string(res)
}
