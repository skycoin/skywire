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
