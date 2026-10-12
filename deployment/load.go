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

// applyServicesJSON sets Prod, Test, ProdConf and TestConf from a
// services-config.
func applyServicesJSON(data []byte) error {
	var env EnvServices
	if err := json.Unmarshal(data, &env); err != nil {
		return err
	}
	var prod, test Services
	var prodConf, testConf Conf
	for _, s := range []struct {
		raw  json.RawMessage
		svc  *Services
		conf *Conf
	}{{env.Prod, &prod, &prodConf}, {env.Test, &test, &testConf}} {
		if s.raw == nil {
			continue
		}
		if err := json.Unmarshal(s.raw, s.svc); err != nil {
			return err
		}
		if err := json.Unmarshal(s.raw, s.conf); err != nil {
			return err
		}
	}
	// services-config.json is dmsg-only (no plain-HTTP deployment URLs except
	// geoip), so the HTTP-named fields come from their dmsg:// siblings.
	prod.BackfillClearnetFromDmsg()
	test.BackfillClearnetFromDmsg()
	if prodConf.Conf == "" {
		prodConf.Conf = prod.ConfDmsg
	}
	if testConf.Conf == "" {
		testConf.Conf = test.ConfDmsg
	}
	Prod, Test, ProdConf, TestConf = prod, test, prodConf, testConf
	ServicesJSON = data
	return nil
}
