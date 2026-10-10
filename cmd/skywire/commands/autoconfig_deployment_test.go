//go:build !js

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncDeploymentDropIn(t *testing.T) {
	dir := t.TempDir()
	deploymentDropIn = filepath.Join(dir, "skywire.service.d", "skywire-deployment.conf")
	svc := filepath.Join(dir, "deployment-services.json")

	if _, err := syncDeploymentDropIn(svc); err == nil {
		t.Fatal("a missing services-config must not be set: the visor would not start")
	}
	if err := os.WriteFile(svc, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := syncDeploymentDropIn(svc); err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v", changed, err)
	}
	b, err := os.ReadFile(deploymentDropIn) //nolint:gosec
	if err != nil || !strings.Contains(string(b), "Environment=SKYDEPLOY="+svc+"\n") {
		t.Fatalf("drop-in %q, %v", b, err)
	}
	if changed, err := syncDeploymentDropIn(svc); err != nil || changed {
		t.Fatalf("rewrite: changed=%v err=%v, want no change", changed, err)
	}
	if changed, err := syncDeploymentDropIn(""); err != nil || !changed {
		t.Fatalf("remove: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(deploymentDropIn); !os.IsNotExist(err) {
		t.Fatalf("drop-in still there: %v", err)
	}
	if changed, err := syncDeploymentDropIn(""); err != nil || changed {
		t.Fatalf("remove again: changed=%v err=%v", changed, err)
	}
}
