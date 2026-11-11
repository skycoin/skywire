package visor

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func TestUI2NodesOfNestsSubHypervisors(t *testing.T) {
	pks := make([]cipher.PubKey, 5)
	for i := range pks {
		pks[i], _ = cipher.GenerateKeyPair()
	}
	sum := func(pk cipher.PubKey, host string) visorapi.Summary {
		return visorapi.Summary{Online: true, Overview: &visorapi.Overview{PubKey: pk, Hostname: host}}
	}
	secs := []VisorTreeSection{
		{HypervisorPK: pks[0], Visors: []visorapi.Summary{sum(pks[0], "hv"), sum(pks[1], "sub"), sum(pks[2], "shared"), sum(pks[4], "broken")}},
		{HypervisorPK: pks[1], Visors: []visorapi.Summary{sum(pks[1], "sub"), sum(pks[2], "shared"), sum(pks[3], "far")}},
		{HypervisorPK: pks[4], SubError: "timeout"},
	}
	ns := ui2NodesOf(secs)
	require.Len(t, ns, 4)
	byPK := map[string]int{}
	for i, n := range ns {
		byPK[n.PK] = i
	}
	sub := ns[byPK[pks[1].Hex()]]
	require.Len(t, sub.Children, 2, "the sub-hypervisor's own row is not repeated under it")
	for _, c := range sub.Children {
		require.Equal(t, c.PK == pks[3].Hex(), c.ViaSub, "only a visor this hypervisor cannot reach links elsewhere")
	}
	require.Equal(t, "timeout", ns[byPK[pks[4].Hex()]].SubErr)
	require.Nil(t, ui2NodesOf(nil))
}
