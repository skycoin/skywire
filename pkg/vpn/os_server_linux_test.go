//go:build linux && !android

// Package vpn pkg/vpn/os_server_linux_test.go c3-app-vpn
package vpn

import (
	"errors"
	"testing"
)

func TestIPTablesForwardPolicyWithoutIPTables(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := GetIPTablesForwardPolicy(); !errors.Is(err, errIPTablesMissing) {
		t.Fatalf("got %v, want errIPTablesMissing", err)
	}
}
