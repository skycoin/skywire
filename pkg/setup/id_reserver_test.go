package setup

import (
	"context"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"github.com/skycoin/dmsg/cipher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
)

// We check the contents of 'idReserver.rec' is as expected after calling 'NewIDReserver'.
// We assume that all dials by the dialer are successful.
func TestNewIDReserver(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	pkC, _ := cipher.GenerateKeyPair()

	type testCase struct {
		paths    [][]routing.Hop         // test input
		expRec   map[cipher.PubKey]uint8 // expected 'idReserver.rec' result
		expTotal int                     // expected 'idReserver.total' result
	}

	testCases := []testCase{
		{
			paths:    nil,
			expRec:   map[cipher.PubKey]uint8{},
			expTotal: 0,
		},
		{
			paths:    [][]routing.Hop{makeHops(pkA, pkB), makeHops(pkB, pkA)},
			expRec:   map[cipher.PubKey]uint8{pkA: 2, pkB: 2},
			expTotal: 4,
		},
		{
			paths:    [][]routing.Hop{makeHops(pkA, pkB, pkC), makeHops(pkC, pkA)},
			expRec:   map[cipher.PubKey]uint8{pkA: 2, pkB: 1, pkC: 2},
			expTotal: 5,
		},
		{
			paths:    [][]routing.Hop{makeHops(pkA, pkB, pkC), makeHops(pkC, pkB, pkA)},
			expRec:   map[cipher.PubKey]uint8{pkA: 2, pkB: 2, pkC: 2},
			expTotal: 6,
		},
	}

	for i, tc := range testCases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			// arrange
			dialer := newMockDialer(t, nil)

			// act
			rtIDR, err := NewIDReserver(context.TODO(), dialer, tc.paths)
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, rtIDR.Close()) })

			// assert
			v := rtIDR.(*idReserver)
			assert.Equal(t, tc.expTotal, v.total)
			assert.Equal(t, tc.expRec, v.rec)
		})
	}
}

// makes a slice of hops from pks
func makeHops(pks ...cipher.PubKey) []routing.Hop {
	hops := make([]routing.Hop, len(pks)-1)
	for i, pk := range pks[:len(pks)-1] {
		hops[i] = routing.Hop{
			TpID: uuid.New(),
			From: pk,
			To:   pks[i+1],
		}
	}
	return hops
}
