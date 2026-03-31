package cliconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseDefault(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"${VAR:-default}", "default"},
		{"${VAR:-}", ""},
		{"${VAR:-false}", "false"},
		{"${VAR:-10}", "10"},
		{"${VAR}", ""},
		{"plain", ""},
		{"${MULTI:-has:-colons}", "has:-colons"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseDefault(tt.input)
			if got != tt.expected {
				t.Errorf("parseDefault(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestScriptExecString(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based tests not applicable on Windows")
	}

	// Create a temp skyenv file that sets TEST_VAR
	dir := t.TempDir()
	envPath := filepath.Join(dir, "test.conf")
	if err := os.WriteFile(envPath, []byte("TEST_VAR='hello world'\n"), 0644); err != nil {
		t.Fatal(err)
	}

	origSkyenv := skyenvfile
	defer func() { skyenvfile = origSkyenv }()

	// Test with a variable that IS set in the env file
	skyenvfile = envPath
	got := scriptExecString("${TEST_VAR}")
	if got != "hello world" {
		t.Errorf("scriptExecString with set var = %q, want %q", got, "hello world")
	}

	// Test with a variable that is NOT set, with a default
	got = scriptExecString("${UNSET_VAR:-mydefault}")
	if got != "mydefault" {
		t.Errorf("scriptExecString with unset var = %q, want %q", got, "mydefault")
	}

	// Test with a variable that is NOT set, no default
	got = scriptExecString("${UNSET_VAR}")
	if got != "" {
		t.Errorf("scriptExecString with unset var no default = %q, want %q", got, "")
	}

	// Test with no env file
	skyenvfile = ""
	got = scriptExecString("${UNSET_VAR:-fallback}")
	if got != "fallback" {
		t.Errorf("scriptExecString with no env file = %q, want %q", got, "fallback")
	}
}

func TestScriptExecBool(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based tests not applicable on Windows")
	}

	dir := t.TempDir()
	envPath := filepath.Join(dir, "test.conf")
	if err := os.WriteFile(envPath, []byte("MYBOOL=true\nMYFALSE=false\n"), 0644); err != nil {
		t.Fatal(err)
	}

	origSkyenv := skyenvfile
	defer func() { skyenvfile = origSkyenv }()
	skyenvfile = envPath

	if got := scriptExecBool("${MYBOOL}"); got != true {
		t.Errorf("scriptExecBool(MYBOOL) = %v, want true", got)
	}
	if got := scriptExecBool("${MYFALSE}"); got != false {
		t.Errorf("scriptExecBool(MYFALSE) = %v, want false", got)
	}
	if got := scriptExecBool("${UNSET:-true}"); got != true {
		t.Errorf("scriptExecBool(UNSET:-true) = %v, want true", got)
	}
	if got := scriptExecBool("${UNSET:-false}"); got != false {
		t.Errorf("scriptExecBool(UNSET:-false) = %v, want false", got)
	}
	if got := scriptExecBool("${UNSET}"); got != false {
		t.Errorf("scriptExecBool(UNSET no default) = %v, want false", got)
	}
}

func TestScriptExecArray(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based tests not applicable on Windows")
	}

	dir := t.TempDir()
	envPath := filepath.Join(dir, "test.conf")
	if err := os.WriteFile(envPath, []byte("MYARR=('aaa' 'bbb' 'ccc')\n"), 0644); err != nil {
		t.Fatal(err)
	}

	origSkyenv := skyenvfile
	defer func() { skyenvfile = origSkyenv }()
	skyenvfile = envPath

	got := scriptExecArray("${MYARR[@]}")
	if got != "aaa,bbb,ccc" {
		t.Errorf("scriptExecArray(MYARR) = %q, want %q", got, "aaa,bbb,ccc")
	}

	// Empty/unset array
	got = scriptExecArray("${UNSETARR[@]}")
	if got != "" {
		t.Errorf("scriptExecArray(UNSETARR) = %q, want %q", got, "")
	}
}

func TestScriptExecInt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash-based tests not applicable on Windows")
	}

	dir := t.TempDir()
	envPath := filepath.Join(dir, "test.conf")
	if err := os.WriteFile(envPath, []byte("MYINT=42\n"), 0644); err != nil {
		t.Fatal(err)
	}

	origSkyenv := skyenvfile
	defer func() { skyenvfile = origSkyenv }()
	skyenvfile = envPath

	if got := scriptExecInt("${MYINT}"); got != 42 {
		t.Errorf("scriptExecInt(MYINT) = %d, want 42", got)
	}
	if got := scriptExecInt("${UNSET:-8}"); got != 8 {
		t.Errorf("scriptExecInt(UNSET:-8) = %d, want 8", got)
	}
	if got := scriptExecInt("${UNSET}"); got != 0 {
		t.Errorf("scriptExecInt(UNSET) = %d, want 0", got)
	}
}
