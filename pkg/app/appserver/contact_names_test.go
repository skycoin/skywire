package appserver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestContactNamesGateway checks the gateway answers from the visor's view for
// the calling app, and that an unwired manager or bare gateway gives nothing.
func TestContactNamesGateway(t *testing.T) {
	m := &procManager{}
	gw := NewRPCGateway(nil, &Proc{m: m, appName: "skychat"})

	var resp map[string]string
	require.NoError(t, gw.ContactNames(&struct{}{}, &resp))
	require.Nil(t, resp)

	m.SetContactNamesFn(func(app string) map[string]string {
		return map[string]string{"pk": "name of " + app}
	})
	require.NoError(t, gw.ContactNames(&struct{}{}, &resp))
	require.Equal(t, map[string]string{"pk": "name of skychat"}, resp)

	require.NoError(t, NewRPCGateway(nil, nil).ContactNames(&struct{}{}, &resp))
}
