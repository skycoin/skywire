//+build !windows

package visor

import (
	"fmt"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/dmsgpty"
)

// DmsgPtyHost extracts DmsgPtyConfig and returns *dmsgpty.Host based on the config.
// If DmsgPtyConfig is not found, DefaultDmsgPtyConfig() is used.
func (c *Config) DmsgPtyHost(dmsgC *dmsg.Client) (*dmsgpty.Host, error) {
	if c.DmsgPty == nil {
		c.DmsgPty = DefaultDmsgPtyConfig()
		if err := c.flush(); err != nil && c.log != nil {
			c.log.WithError(err).Errorf("Failed to flush config to disk")
		}
	}

	var wl dmsgpty.Whitelist
	if c.DmsgPty.AuthFile == "" {
		wl = dmsgpty.NewMemoryWhitelist()
	} else {
		var err error
		if wl, err = dmsgpty.NewJSONFileWhiteList(c.DmsgPty.AuthFile); err != nil {
			return nil, err
		}
	}

	// Whitelist hypervisor PKs.
	hypervisorWL := dmsgpty.NewMemoryWhitelist()
	for _, hv := range c.Hypervisors {
		if err := hypervisorWL.Add(hv.PubKey); err != nil {
			return nil, fmt.Errorf("failed to add hypervisor PK to whitelist: %v", err)
		}
	}

	host := dmsgpty.NewHost(dmsgC, dmsgpty.NewCombinedWhitelist(0, wl, hypervisorWL))
	return host, nil
}
