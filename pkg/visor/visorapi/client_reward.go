package visorapi

import (
	"errors"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
)

// ErrSingleRewardUnsupported is returned when the visor, or the hypervisor
// in between, runs a build without SetSingleRewardAddress.
var ErrSingleRewardUnsupported = errors.New("this visor's build cannot set a single reward address with the opt-in. " +
	"Update the visor (and its hypervisor), or set an account xpub")

func singleRewardErr(err error) error {
	if err != nil && strings.Contains(err.Error(), "can't find method") {
		return ErrSingleRewardUnsupported
	}
	return err
}

// SetSingleRewardAddress implements API.
func (rc *rpcClient) SetSingleRewardAddress(addr string) (string, error) {
	var out string
	err := rc.Call("SetSingleRewardAddress", &addr, &out)
	return out, singleRewardErr(err)
}

// HVSetSingleRewardAddress sets a single reward address, with the opt-in, on a remote visor.
func (rc *rpcClient) HVSetSingleRewardAddress(pk cipher.PubKey, addr string) (string, error) {
	var out string
	err := rc.Call("HVSetSingleRewardAddress", &HVRewardArgs{PK: pk, Addr: addr}, &out)
	return out, singleRewardErr(err)
}
