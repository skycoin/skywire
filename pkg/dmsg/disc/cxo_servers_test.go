package disc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestCXOServersClient(t *testing.T) {
	ctx := context.Background()
	primary := NewMock(0)
	pk, sk := cipher.GenerateKeyPair()
	e := &Entry{Static: pk, Version: "0.0.1", Timestamp: time.Now().UnixNano(), Server: &Server{Address: "1.1.1.1:80"}}
	require.NoError(t, e.Sign(sk))
	require.NoError(t, primary.PostEntry(ctx, e))

	fromFeed := &Entry{Static: pk, Server: &Server{Address: "2.2.2.2:80"}}
	hasAnswer := true
	c := NewCXOServersClient(primary, func(context.Context) ([]*Entry, bool) {
		if !hasAnswer {
			return nil, false
		}
		return []*Entry{fromFeed}, true
	})
	got, err := c.AllServers(ctx)
	require.NoError(t, err)
	require.Equal(t, "2.2.2.2:80", got[0].Server.Address, "answered from the feed")

	hasAnswer = false
	got, err = c.AllServers(ctx)
	require.NoError(t, err)
	require.Equal(t, "1.1.1.1:80", got[0].Server.Address, "no feed answer asks the discovery")

	require.Equal(t, primary, NewCXOServersClient(primary, nil))
}
