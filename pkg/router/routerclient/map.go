// Package routerclient pkg/router/routerclient/map.go
package routerclient

import (
	"context"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// Map is a map of router RPC clients associated with the router's visor PK.
type Map map[cipher.PubKey]*Client

type dialResult struct {
	client *Client
	err    error
}

// MakeMap makes a Map of the router clients, where the key is the router's visor public key.
// It creates these router clients by dialing to them concurrently.
func MakeMap(ctx context.Context, dialer network.Dialer, pks []cipher.PubKey) (Map, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan dialResult)
	defer close(results)

	for _, pk := range pks {
		go func(pk cipher.PubKey) {
			client, err := NewClient(ctx, dialer, pk)
			results <- dialResult{client: client, err: err}
		}(pk)
	}

	rcM := make(Map, len(pks))
	var err error
	for range pks {
		res := <-results
		if isDone(ctx) {
			continue
		}
		if res.err != nil {
			cancel()
			err = res.err
			continue
		}
		rcM[res.client.rPK] = res.client
	}

	if err != nil {
		rcM.CloseAll() // TODO: log this
	}
	return rcM, err
}

// Client returns a router client of given public key.
func (cm Map) Client(rPK cipher.PubKey) *Client {
	return cm[rPK]
}

// CloseAll closes all contained router clients.
func (cm Map) CloseAll() (errs []error) {
	for k, c := range cm {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
		delete(cm, k)
	}
	return errs
}

func isDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
