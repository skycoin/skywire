package dmsgpty

import (
	"io"
	"net/rpc"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// WhitelistClient interacts with a whitelist's API.
type WhitelistClient struct {
	c *rpc.Client
}

// NewWhitelistClient creates a new whitelist client.
func NewWhitelistClient(conn io.ReadWriteCloser) (*WhitelistClient, error) {
	if err := writeRequest(conn, WhitelistURI); err != nil {
		return nil, err
	}
	if err := readResponse(conn); err != nil {
		return nil, err
	}
	return &WhitelistClient{c: rpc.NewClient(conn)}, nil
}

// ViewWhitelist obtains the whitelist entries from host.
func (wc WhitelistClient) ViewWhitelist() ([]cipher.PubKey, error) {
	var pks []cipher.PubKey
	err := wc.c.Call(wc.rpcMethod("Whitelist"), &empty, &pks)
	return pks, err
}

// WhitelistAdd adds a whitelist entry to host.
func (wc WhitelistClient) WhitelistAdd(pks ...cipher.PubKey) error {
	return wc.c.Call(wc.rpcMethod("WhitelistAdd"), &pks, &empty)
}

// WhitelistRemove removes a whitelist entry from host.
func (wc WhitelistClient) WhitelistRemove(pks ...cipher.PubKey) error {
	return wc.c.Call(wc.rpcMethod("WhitelistRemove"), &pks, &empty)
}

func (*WhitelistClient) rpcMethod(m string) string {
	return WhitelistRPCName + "." + m
}
