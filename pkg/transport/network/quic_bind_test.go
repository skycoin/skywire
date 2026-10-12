//go:build !(js && wasm)

package network

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// The QUIC bind loop must end when the client closes, whether the resolver
// request is in flight or the loop is waiting to retry. It used to outlive
// Close by a request plus a backoff, or for good when the request hung.
func TestQUICBindLoopEndsOnClose(t *testing.T) {
	for name, bind := range map[string]func(mock.Arguments){
		"request in flight": func(args mock.Arguments) { <-args.Get(0).(context.Context).Done() },
		"waiting to retry":  func(mock.Arguments) {},
	} {
		t.Run(name, func(t *testing.T) {
			ar := &addrresolver.MockAPIClient{}
			called := make(chan struct{}, 16)
			ar.On("BindQUIC", mock.Anything, "1").Run(func(args mock.Arguments) {
				called <- struct{}{}
				bind(args)
			}).Return(errors.New("resolver unreachable"))
			c := &quicClient{resolvedClient: &resolvedClient{genericClient: newTestGenericClient(t, types.QUIC), ar: ar}}

			ended := make(chan struct{})
			go func() {
				c.bindAndReRegister("1")
				close(ended)
			}()
			<-called
			time.Sleep(100 * time.Millisecond)
			require.NoError(t, c.genericClient.Close())
			select {
			case <-ended:
			case <-time.After(2 * time.Second):
				t.Fatal("the bind loop outlived Close")
			}
		})
	}
}
