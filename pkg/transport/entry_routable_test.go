package transport

import (
	"testing"

	"github.com/stretchr/testify/require"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

func TestRoutableDropsLocalLinks(t *testing.T) {
	a := &Entry{Type: types.STCPR}
	b := &Entry{Type: types.LOCAL}
	c := &Entry{Type: types.SUDPH}
	in := []*Entry{a, b, c}
	require.Equal(t, []*Entry{a, c}, Routable(in))
	require.Equal(t, []*Entry{a, b, c}, in, "the input is not modified")

	plain := []*Entry{a, c}
	out := Routable(plain)
	require.Equal(t, plain, out)
	require.Same(t, &plain[0], &out[0], "nothing to drop means no copy")
}
