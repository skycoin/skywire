package ui2

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func TestCatalogOf(t *testing.T) {
	c := CatalogOf("pk", []visorapi.CatalogEntry{
		{Name: "vpn-client", Kind: "client"},
		{Name: "odd", Kind: "weird"},
		{Name: "vpn-server", Kind: "server"},
	})
	var kinds []string
	for _, g := range c.Groups {
		kinds = append(kinds, g.Kind)
	}
	require.Equal(t, []string{"server", "client", "external"}, kinds, "an unknown kind is external and empty groups are dropped")

	e := visorapi.CatalogEntry{Name: "skysocks-client", Instances: []visorapi.CatalogInstance{{Name: "skysocks-client"}, {Name: "skysocks-client-2"}}}
	require.Equal(t, "skysocks-client-3", NextInstanceName(e))
	require.Equal(t, 3, instanceStatus("starting"))
	require.Equal(t, 0, instanceStatus("stopped"))
}
