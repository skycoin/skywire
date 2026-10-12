package dmsgc

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// A visor whose own server is the only one it knows must connect to it, or its
// client never becomes ready. One that knows another server skips itself.
func TestSkipSelfServer(t *testing.T) {
	self, _ := cipher.GenerateKeyPair()
	other := serverEntry("198.51.100.1:8080")
	own := &disc.Entry{Static: self, Server: &disc.Server{Address: "203.0.113.7:8080"}}
	on := &DmsgServerConfig{Enabled: true}

	for name, tc := range map[string]struct {
		conf *DmsgConfig
		want bool
	}{
		"no server":             {&DmsgConfig{Servers: []*disc.Entry{own}}, false},
		"server disabled":       {&DmsgConfig{Servers: []*disc.Entry{own}, Server: &DmsgServerConfig{}}, false},
		"only self":             {&DmsgConfig{Servers: []*disc.Entry{own}, Server: on}, false},
		"self listed twice":     {&DmsgConfig{Servers: []*disc.Entry{own, own}, Server: on}, false},
		"self and another":      {&DmsgConfig{Servers: []*disc.Entry{own, other}, Server: on}, true},
		"another only":          {&DmsgConfig{Servers: []*disc.Entry{other}, Server: on}, true},
		"another on the LAN":    {&DmsgConfig{Servers: []*disc.Entry{own}, LANServers: []*disc.Entry{other}, Server: on}, true},
		"no servers configured": {&DmsgConfig{Server: on}, true},
		"another in deployment 2": {&DmsgConfig{Server: on, Deployments: []Deployment{
			{Servers: []*disc.Entry{own}}, {Servers: []*disc.Entry{other}}}}, true},
	} {
		if got := SkipSelfServer(self, tc.conf); got != tc.want {
			t.Errorf("%s: SkipSelfServer = %v, want %v", name, got, tc.want)
		}
	}
}
