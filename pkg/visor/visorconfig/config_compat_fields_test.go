package visorconfig

import "testing"

// The mirror test checks v1JSON's fields; this checks UnmarshalJSON copies
// one across.
func TestDeploymentStatusAddrRoundTrip(t *testing.T) {
	var v V1
	if err := v.UnmarshalJSON([]byte(`{"deployment_status_addr":"127.0.0.1:8092"}`)); err != nil {
		t.Fatal(err)
	}
	if v.DeploymentStatusAddr != "127.0.0.1:8092" {
		t.Fatalf("deployment_status_addr read as %q", v.DeploymentStatusAddr)
	}
}
