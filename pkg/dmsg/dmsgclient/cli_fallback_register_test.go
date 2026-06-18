package dmsgclient

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// recordingDisc embeds disc.APIClient so only the methods exercised below need
// implementing; any other call would nil-panic (and would be a test bug).
type recordingDisc struct {
	disc.APIClient
	entry            *disc.Entry // returned by Entry when non-nil
	puts, posts, del int
	entryCalls       int
}

func (r *recordingDisc) Entry(_ context.Context, pk cipher.PubKey) (*disc.Entry, error) {
	r.entryCalls++
	if r.entry != nil && r.entry.Static == pk {
		return r.entry, nil
	}
	return &disc.Entry{}, nil // not found (Static != pk)
}
func (r *recordingDisc) PutEntry(_ context.Context, _ cipher.SecKey, _ *disc.Entry) error {
	r.puts++
	return nil
}
func (r *recordingDisc) PostEntry(_ context.Context, _ *disc.Entry) error { r.posts++; return nil }
func (r *recordingDisc) DelEntry(_ context.Context, _ *disc.Entry) error  { r.del++; return nil }

// TestFallbackDiscClient_WriteRouting is the crux of the single-client
// convergence: the registering variant must publish writes to HTTP discovery
// (so the visor registers) while the service variant no-ops via direct.
func TestFallbackDiscClient_WriteRouting(t *testing.T) {
	log := logging.MustGetLogger("test")
	pk, sk := cipher.GenerateKeyPair()
	e := &disc.Entry{Static: pk}

	t.Run("service variant writes go to direct (no-op)", func(t *testing.T) {
		d, h := &recordingDisc{}, &recordingDisc{}
		c := NewFallbackDiscClient(d, h, log)
		require.NoError(t, c.PutEntry(context.Background(), sk, e))
		require.NoError(t, c.PostEntry(context.Background(), e))
		require.NoError(t, c.DelEntry(context.Background(), e))
		assert.Equal(t, 1, d.puts)
		assert.Equal(t, 1, d.posts)
		assert.Equal(t, 1, d.del)
		assert.Zero(t, h.puts+h.posts+h.del, "service variant must NOT publish to HTTP discovery")
	})

	t.Run("registering: Put/DelEntry publish to HTTP, PostEntry no-ops", func(t *testing.T) {
		d, h := &recordingDisc{}, &recordingDisc{}
		c := NewRegisteringFallbackDiscClient(d, h, log)
		require.NoError(t, c.PutEntry(context.Background(), sk, e))
		require.NoError(t, c.PostEntry(context.Background(), e))
		require.NoError(t, c.DelEntry(context.Background(), e))
		assert.Equal(t, 1, h.puts, "PutEntry = durable post-session registration")
		assert.Equal(t, 1, h.del)
		// PostEntry ALWAYS no-ops (direct): it's the PRE-session call that would
		// otherwise wedge the dmsg serve loop. PutEntry (post-session) registers.
		assert.Equal(t, 1, d.posts, "PostEntry no-ops via direct even when registering")
		assert.Zero(t, h.posts, "PostEntry must NOT block-publish pre-session")
		assert.Zero(t, d.puts+d.del, "Put/DelEntry must NOT hit the direct no-op client")
	})
}

// TestFallbackDiscClient_ReadDirectFirst confirms reads resolve direct-first in
// BOTH variants — that's what keeps seeded service/dmsg-disc PKs off the
// HTTP-over-dmsg round-trip that hot-loops.
func TestFallbackDiscClient_ReadDirectFirst(t *testing.T) {
	log := logging.MustGetLogger("test")
	pk, _ := cipher.GenerateKeyPair()
	seeded := &disc.Entry{Static: pk}

	for _, tc := range []struct {
		name string
		ctor func(direct, http disc.APIClient, log *logging.Logger) disc.APIClient
	}{
		{"service", NewFallbackDiscClient},
		{"registering", NewRegisteringFallbackDiscClient},
	} {
		t.Run(tc.name+" hit: direct resolves, http untouched", func(t *testing.T) {
			d, h := &recordingDisc{entry: seeded}, &recordingDisc{}
			c := tc.ctor(d, h, log)
			got, err := c.Entry(context.Background(), pk)
			require.NoError(t, err)
			assert.Equal(t, pk, got.Static)
			assert.Equal(t, 1, d.entryCalls)
			assert.Zero(t, h.entryCalls, "a direct (seeded) hit must NOT round-trip to HTTP")
		})

		t.Run(tc.name+" miss: falls back to http", func(t *testing.T) {
			d, h := &recordingDisc{}, &recordingDisc{entry: seeded} // direct misses, http has it
			c := tc.ctor(d, h, log)
			got, err := c.Entry(context.Background(), pk)
			require.NoError(t, err)
			assert.Equal(t, pk, got.Static)
			assert.Equal(t, 1, d.entryCalls)
			assert.Equal(t, 1, h.entryCalls, "an unknown PK must fall back to HTTP discovery")
		})
	}
}
