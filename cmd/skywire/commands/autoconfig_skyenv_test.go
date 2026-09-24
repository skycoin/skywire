// Package commands cmd/skywire/commands/autoconfig_skyenv_test.go c4-vis-cli
package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureSkyenvFileCreatesTheTemplateWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "etc", "skywire.conf")

	created, err := ensureSkyenvFile(path, true)
	if err != nil {
		t.Fatalf("ensureSkyenvFile: %v", err)
	}
	if !created {
		t.Fatal("created = false, want true for an absent file")
	}

	b, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	got := string(b)

	// It must be the real template, not a stub: the knobs are the point.
	for _, want := range []string{"SKYWIRE CONFIG TEMPLATE", "#ISHYPERVISOR=true", "#VISORISPUBLIC=true"} {
		if !strings.Contains(got, want) {
			t.Errorf("template is missing %q", want)
		}
	}
	// PKGENV requested, so that one line is live and USRENV stays commented.
	if !strings.Contains(got, "\nPKGENV=true") {
		t.Error("PKGENV=true was not uncommented")
	}
	if !strings.Contains(got, "#USRENV=true") {
		t.Error("USRENV should have stayed commented")
	}
}

func TestEnsureSkyenvFileUncommentsUsrEnvInstead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skywire.conf")

	if _, err := ensureSkyenvFile(path, false); err != nil {
		t.Fatalf("ensureSkyenvFile: %v", err)
	}

	b, _ := os.ReadFile(path) //nolint:gosec,errcheck
	got := string(b)
	if !strings.Contains(got, "\nUSRENV=true") {
		t.Error("USRENV=true was not uncommented")
	}
	if !strings.Contains(got, "#PKGENV=true") {
		t.Error("PKGENV should have stayed commented")
	}
}

// The whole safety property: an existing file is never touched, however it got
// there and whatever the operator has since done to it.
func TestEnsureSkyenvFileNeverOverwritesAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skywire.conf")
	const operatorEdits = "# my notes\nISHYPERVISOR=true\nVISORISPUBLIC=true\n"
	if err := os.WriteFile(path, []byte(operatorEdits), 0600); err != nil {
		t.Fatal(err)
	}

	created, err := ensureSkyenvFile(path, true)
	if err != nil {
		t.Fatalf("ensureSkyenvFile: %v", err)
	}
	if created {
		t.Error("created = true, want false for an existing file")
	}

	b, _ := os.ReadFile(path) //nolint:gosec,errcheck
	if string(b) != operatorEdits {
		t.Errorf("existing file was modified:\n%s", string(b))
	}
}

func TestEnsureSkyenvFileIgnoresAnEmptyPath(t *testing.T) {
	created, err := ensureSkyenvFile("", true)
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if created {
		t.Error("created = true for an empty path")
	}
}

// Calling it twice must be a no-op the second time — autoconfig runs on every
// boot of the wasm desk, so this runs constantly.
func TestEnsureSkyenvFileIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skywire.conf")

	if created, err := ensureSkyenvFile(path, true); err != nil || !created {
		t.Fatalf("first call: created=%v err=%v", created, err)
	}
	first, _ := os.ReadFile(path) //nolint:gosec,errcheck

	created, err := ensureSkyenvFile(path, true)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if created {
		t.Error("second call reported created = true")
	}
	second, _ := os.ReadFile(path) //nolint:gosec,errcheck
	if string(first) != string(second) {
		t.Error("second call changed the file")
	}
}
