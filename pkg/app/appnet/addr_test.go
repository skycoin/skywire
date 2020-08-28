package appnet

import (
	"net"
	"testing"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
)

func TestConvertAddr(t *testing.T) {
	type want struct {
		addr Addr
		err  error
	}

	pk, _ := cipher.GenerateKeyPair()

	const port uint16 = 100

	tt := []struct {
		name string
		addr net.Addr
		want want
	}{
		{
			name: "ok - dmsg addr",
			addr: dmsg.Addr{
				PK:   pk,
				Port: port,
			},
			want: want{
				addr: Addr{
					Net:    TypeDmsg,
					PubKey: pk,
					Port:   routing.Port(port),
				},
			},
		},
		{
			name: "ok - routing addr",
			addr: routing.Addr{
				PubKey: pk,
				Port:   routing.Port(port),
			},
			want: want{
				addr: Addr{
					Net:    TypeSkynet,
					PubKey: pk,
					Port:   routing.Port(port),
				},
			},
		},
	}

	for _, tc := range tt {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			addr, err := ConvertAddr(tc.addr)
			require.Equal(t, err, tc.want.err)
			if err != nil {
				return
			}
			require.Equal(t, addr, tc.want.addr)
		})
	}
}
