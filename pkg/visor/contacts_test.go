package visor

import (
	"net"
	"path/filepath"
	"testing"
	"time"

	rpc "github.com/0magnet/gobrpc"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/contacts"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// TestContactsOverRPC drives the address book through the generated RPC
// registration and the CLI's client, as `skywire cli contacts` does.
func TestContactsOverRPC(t *testing.T) {
	root := t.TempDir()
	v := &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{}}}
	v.conf.LocalPath = filepath.Join(root, "local")

	srv := rpc.NewServer()
	require.NoError(t, registerVisorRPC(srv, visorapi.RPCPrefix, &RPC{visor: v, log: logrus.New()}))
	sc, cc := net.Pipe()
	go srv.ServeConn(sc)
	api := visorapi.NewRPCClient(nil, cc, visorapi.RPCPrefix, 30*time.Second)

	pk, _ := cipher.GenerateKeyPair()
	alice, err := api.ContactsPut(contacts.Contact{
		Name: "Alice", Keys: []cipher.PubKey{pk},
		Payments: []contacts.Payment{{Coin: "skycoin", Address: "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"}},
	})
	require.NoError(t, err)
	require.NotEmpty(t, alice.UID)
	require.FileExists(t, filepath.Join(root, "contacts", alice.UID+".vcf"), "the book is beside the local path")

	list, err := api.ContactsList()
	require.NoError(t, err)
	require.Equal(t, []contacts.Contact{*alice}, list)

	vcf, err := api.ContactsExport()
	require.NoError(t, err)
	require.NoError(t, api.ContactsDelete(alice.UID))
	require.Error(t, api.ContactsDelete(alice.UID), "deleting twice")
	n, err := api.ContactsImport(vcf)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	list, err = api.ContactsList()
	require.NoError(t, err)
	require.Equal(t, []contacts.Contact{*alice}, list, "export, delete, import gives the contact back")

	_, err = api.ContactsPut(contacts.Contact{})
	require.Error(t, err, "a contact needs a name")
}
