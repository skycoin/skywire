// Package deployment deployment/load.go
package deployment

import (
	"encoding/json"
	"fmt"
	"os"
)

// LoadFile replaces the deployment with the services-config at path, as
// SKYDEPLOY does at start. A browser visor has no environment at start, so
// autoconfig calls it there once it has read SKYDEPLOY from skywire.conf.
func LoadFile(path string) error {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return err
	}
	if err := applyServicesJSON(data); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// applyServicesJSON sets Prod and ProdConf from a services-config.
func applyServicesJSON(data []byte) error {
	var env EnvServices
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	var prod Services
	var prodConf Conf
	if env.Prod != nil {
		if err := json.Unmarshal(env.Prod, &prod); err != nil {
			return err
		}
		if err := json.Unmarshal(env.Prod, &prodConf); err != nil {
			return err
		}
	}
	// services-config.json is dmsg-only (no plain-HTTP deployment URLs except
	// geoip), so the HTTP-named fields come from their dmsg:// siblings.
	prod.BackfillClearnetFromDmsg()
	if prodConf.Conf == "" {
		prodConf.Conf = prod.ConfDmsg
	}
	Prod, ProdConf = prod, prodConf
	ServicesJSON = data
	return nil
}
