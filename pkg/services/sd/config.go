// Package sd pkg/services/sd/config.go c2-vis-appsvc
package sd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/services"
)

// Config is the JSON configuration for service-discovery.
type Config struct {
	Path string `json:"-"`

	services.Common

	Whitelist []string `json:"whitelist_keys,omitempty"`

	// GeoIP is the URL of the geoip service. Visors populate Geo on
	// their entries directly post-#2439; field kept for backward
	// compatibility.
	GeoIP string `json:"geoip,omitempty"`

	// Dmsg is the dmsg-related config block — same shape across
	// every deployment service that uses it.
	Dmsg cmdutil.DmsgConfig `json:"dmsg,omitempty"`

	// ChartsAddr serves only the charts page over plain HTTP. Empty disables it.
	ChartsAddr string `json:"charts_addr,omitempty"`
}

// LoadFile reads and strict-parses a Config from path.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	var c Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	c.Path = path
	return &c, nil
}

// ParseBlock decodes a services.json block into a Config.
func ParseBlock(raw []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("service-discovery: parse block: %w", err)
	}
	return &c, nil
}

func dmsgDiscEntries(configServers []*disc.Entry) []disc.Entry {
	if len(configServers) == 0 {
		return dmsg.Prod.DmsgServers
	}
	out := make([]disc.Entry, 0, len(configServers))
	for _, e := range configServers {
		if e != nil {
			out = append(out, *e)
		}
	}
	return out
}
