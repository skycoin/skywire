package visor

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// subHVAPI records the forwarded app calls of a sub-hypervisor.
type subHVAPI struct {
	visorapi.API
	pk    cipher.PubKey
	added []string
}

func (s *subHVAPI) HVVisorSummary(cipher.PubKey) (*visorapi.Summary, error) {
	return &visorapi.Summary{Overview: &visorapi.Overview{Apps: []*appserver.AppState{{AppConfig: appserver.AppConfig{Name: "skysocks"}}}}}, nil
}

func (s *subHVAPI) HVAppCatalog(pk cipher.PubKey) ([]visorapi.CatalogEntry, error) {
	s.pk = pk
	return []visorapi.CatalogEntry{{Name: "skysocks"}}, nil
}

func (s *subHVAPI) HVAddApp(pk cipher.PubKey, name, binary string) error {
	s.pk = pk
	s.added = append(s.added, name+"="+binary)
	return nil
}

func TestProxiedVisorAppCalls(t *testing.T) {
	target, _ := cipher.GenerateKeyPair()
	sub := &subHVAPI{}
	p := newProxiedVisorAPI(target, sub)

	cat, err := p.AppCatalog()
	require.NoError(t, err)
	require.Equal(t, "skysocks", cat[0].Name)
	require.Equal(t, target, sub.pk)

	require.NoError(t, p.AddApp("skysocks-2", "skysocks"))
	require.Equal(t, []string{"skysocks-2=skysocks"}, sub.added)

	app, err := p.App("skysocks")
	require.NoError(t, err)
	require.Equal(t, "skysocks", app.Name)
	_, err = p.App("missing")
	require.Error(t, err)
}
