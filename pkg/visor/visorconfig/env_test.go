package visorconfig

import (
	"testing"

	"github.com/skycoin/skywire/pkg/app/appserver"
)

func TestUpdateEnvEntry(t *testing.T) {
	makeLauncher := func(env []string) *Launcher {
		return &Launcher{
			Apps: appsList{appserver.AppConfig{Name: "skycoin-daemon-aix", Env: env}},
		}
	}

	t.Run("add when missing", func(t *testing.T) {
		l := makeLauncher(nil)
		if !updateEnvEntry(l, "skycoin-daemon-aix", "FIBER_TOML", "/etc/aix.toml") {
			t.Fatal("expected change")
		}
		got := l.Apps[0].Env
		if len(got) != 1 || got[0] != "FIBER_TOML=/etc/aix.toml" {
			t.Fatalf("unexpected env: %v", got)
		}
	})

	t.Run("replace existing", func(t *testing.T) {
		l := makeLauncher([]string{"FIBER_TOML=/old.toml", "HOME=/var/skycoin"})
		if !updateEnvEntry(l, "skycoin-daemon-aix", "FIBER_TOML", "/new.toml") {
			t.Fatal("expected change")
		}
		got := l.Apps[0].Env
		if len(got) != 2 || got[0] != "FIBER_TOML=/new.toml" || got[1] != "HOME=/var/skycoin" {
			t.Fatalf("unexpected env: %v", got)
		}
	})

	t.Run("delete by empty value", func(t *testing.T) {
		l := makeLauncher([]string{"FIBER_TOML=/x.toml", "HOME=/var/skycoin"})
		if !updateEnvEntry(l, "skycoin-daemon-aix", "FIBER_TOML", "") {
			t.Fatal("expected change")
		}
		got := l.Apps[0].Env
		if len(got) != 1 || got[0] != "HOME=/var/skycoin" {
			t.Fatalf("unexpected env: %v", got)
		}
	})

	t.Run("noop when no change", func(t *testing.T) {
		l := makeLauncher([]string{"FIBER_TOML=/x.toml"})
		if updateEnvEntry(l, "skycoin-daemon-aix", "FIBER_TOML", "/x.toml") {
			t.Fatal("expected no change")
		}
	})

	t.Run("noop deleting missing key", func(t *testing.T) {
		l := makeLauncher(nil)
		if updateEnvEntry(l, "skycoin-daemon-aix", "FIBER_TOML", "") {
			t.Fatal("expected no change")
		}
	})

	t.Run("app not found", func(t *testing.T) {
		l := makeLauncher(nil)
		if updateEnvEntry(l, "missing-app", "FIBER_TOML", "/x.toml") {
			t.Fatal("expected no change for missing app")
		}
	})
}
