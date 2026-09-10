//go:build !js

package spec

import (
	"encoding/json"
	"testing"
)

// The visor-wide `server` block rides the single-deployment object shape;
// config_path must survive a marshal/unmarshal cycle like the other fields.
func TestDmsgServerConfig_ConfigPathRoundTrip(t *testing.T) {
	in := DmsgConfig{
		Discovery: "dmsg://0208f9b6b6bd2fcf9c6ac1fa8a7bde5b5ad3a3e2d2f0f8a4d7e0c1b2a3d4e5f6a7:80",
		Server:    &DmsgServerConfig{Enabled: true, ConfigPath: "/etc/skywire-dmsg.json"},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out DmsgConfig
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if out.Server == nil || !out.Server.Enabled || out.Server.ConfigPath != in.Server.ConfigPath {
		t.Fatalf("server block lost: %+v\n%s", out.Server, b)
	}
}
