// Package clivisor cmd/skywire-cli/commands/visor/hv_load_test.go
package clivisor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor"
)

func TestHvLsHeader_LoadColumns(t *testing.T) {
	base := hvLsHeader(false)
	require.NotContains(t, base, "LOAD")
	require.NotContains(t, base, "MEM%")

	withLoad := hvLsHeader(true)
	require.True(t, strings.HasPrefix(withLoad, base), "load header must extend the base header")
	require.Equal(t, base+"\tLOAD\tMEM%\tDISK%", withLoad)
}

func TestFmtLoadCells(t *testing.T) {
	tests := []struct {
		name string
		e    visor.HVVisorEntry
		want string
	}{
		{
			name: "nil load → dashes",
			e:    visor.HVVisorEntry{},
			want: "-\t-\t-",
		},
		{
			name: "with cores → load/cores",
			e:    visor.HVVisorEntry{Load: &visor.LoadStats{Load1: 9.43, CPUCores: 4, MemUsedPercent: 35.2, DiskUsedPercent: 100}},
			want: "9.43/4\t35%\t100%",
		},
		{
			name: "no cores → bare load",
			e:    visor.HVVisorEntry{Load: &visor.LoadStats{Load1: 1.07, CPUCores: 0, MemUsedPercent: 12.9, DiskUsedPercent: 48.4}},
			want: "1.07\t13%\t48%",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, fmtLoadCells(tt.e))
		})
	}
}
