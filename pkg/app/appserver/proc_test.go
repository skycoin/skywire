// Package appserver pkg/app/appserver/proc_test.go
package appserver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProc_DetailedStatus(t *testing.T) {
	p := &Proc{}

	wantStatus := "status"
	p.status = wantStatus

	gotStatus := p.DetailedStatus()
	require.Equal(t, wantStatus, gotStatus)
}

func TestProc_SetDetailedStatus(t *testing.T) {
	p := &Proc{}

	status := "status"

	p.SetDetailedStatus(status)

	p.statusMx.RLock()
	defer p.statusMx.RUnlock()
	require.Equal(t, status, p.status)
}

func TestProc_SetOTP(t *testing.T) {
	p := &Proc{}

	require.Empty(t, p.OTP())

	p.SetOTP("ABC123")
	require.Equal(t, "ABC123", p.OTP())

	// Rotating replaces the previous code — a consumed OTP must not linger.
	p.SetOTP("XYZ789")
	require.Equal(t, "XYZ789", p.OTP())

	// The OTP is independent of detailed status: publishing one must not
	// disturb the status the app list renders (and vice versa). Uses
	// "Starting" rather than "Running" because the latter closes readyCh,
	// which a bare &Proc{} hasn't allocated.
	p.SetDetailedStatus(AppDetailedStatusStarting)
	require.Equal(t, "XYZ789", p.OTP())
	require.Equal(t, AppDetailedStatusStarting, p.DetailedStatus())
}
