package ptycfg

import (
	"io"
	"net/rpc"

	"github.com/SkycoinProject/dmsg/cipher"
)

// Used for RPC calls
var empty struct{}

// ViewWhitelist obtains the whitelist entries from host.
func ViewWhitelist(conn io.ReadWriteCloser) ([]cipher.PubKey, error) {
	var pks []cipher.PubKey
	err := rpc.NewClient(conn).Call(rpcMethod("Whitelist"), &empty, &pks)
	return pks, err
}

// WhitelistAdd adds a whitelist entry to host.
func WhitelistAdd(conn io.ReadWriteCloser, pks ...cipher.PubKey) error {
	return rpc.NewClient(conn).Call(rpcMethod("WhitelistAdd"), &pks, &empty)
}

// WhitelistRemove removes a whitelist entry from host.
func WhitelistRemove(conn io.ReadWriteCloser, pks ...cipher.PubKey) error {
	return rpc.NewClient(conn).Call(rpcMethod("WhitelistRemove"), &pks, &empty)
}

func rpcMethod(m string) string {
	return GatewayName + "." + m
}
