// Package dmsgclient dmsgclient_test.go: unit tests for flag parsing,
// the caching/fallback discovery-client wrappers, and the fallback HTTP
// round tripper. The dmsg-server-backed Start* paths are integration
// territory and are not exercised here.
package dmsgclient

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

func testLog() *logging.Logger { return logging.MustGetLogger("dmsgclient_test") }

// ---- recording fake disc.APIClient ----------------------------------------

type fakeDisc struct {
	mu       sync.Mutex
	called   map[string]int
	entry    *disc.Entry
	entryErr error
}

func (f *fakeDisc) mark(m string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.called == nil {
		f.called = map[string]int{}
	}
	f.called[m]++
}

func (f *fakeDisc) count(m string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.called[m]
}

func (f *fakeDisc) Entry(_ context.Context, _ cipher.PubKey) (*disc.Entry, error) {
	f.mark("Entry")
	return f.entry, f.entryErr
}
func (f *fakeDisc) PostEntry(_ context.Context, _ *disc.Entry) error { f.mark("PostEntry"); return nil }
func (f *fakeDisc) PutEntry(_ context.Context, _ cipher.SecKey, _ *disc.Entry) error {
	f.mark("PutEntry")
	return nil
}
func (f *fakeDisc) DelEntry(_ context.Context, _ *disc.Entry) error { f.mark("DelEntry"); return nil }
func (f *fakeDisc) AvailableServers(_ context.Context) ([]*disc.Entry, error) {
	f.mark("AvailableServers")
	return nil, nil
}
func (f *fakeDisc) AllServers(_ context.Context) ([]*disc.Entry, error) {
	f.mark("AllServers")
	return nil, nil
}
func (f *fakeDisc) AllEntries(_ context.Context) ([]string, error) {
	f.mark("AllEntries")
	return nil, nil
}
func (f *fakeDisc) AllClientsByServer(_ context.Context) (map[string][]*disc.Entry, error) {
	f.mark("AllClientsByServer")
	return nil, nil
}
func (f *fakeDisc) ClientsByServer(_ context.Context, _ cipher.PubKey) ([]*disc.Entry, error) {
	f.mark("ClientsByServer")
	return nil, nil
}

// ---- ParseServerAddr -------------------------------------------------------

func TestParseServerAddr(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	t.Run("valid", func(t *testing.T) {
		entry, err := ParseServerAddr(pk.Hex() + "@1.2.3.4:8080")
		require.NoError(t, err)
		require.Equal(t, pk, entry.Static)
		require.Equal(t, "1.2.3.4:8080", entry.Server.Address)
		require.Equal(t, "0.0.1", entry.Version)
	})

	for _, bad := range []string{
		"",
		"no-at-sign",
		"@1.2.3.4:8080",       // empty pk
		pk.Hex() + "@",        // empty addr
		"nothex@1.2.3.4:8080", // invalid pk
	} {
		t.Run("invalid/"+bad, func(t *testing.T) {
			_, err := ParseServerAddr(bad)
			require.Error(t, err)
		})
	}
}

// ---- InitFlags / InitConfig / ExecName ------------------------------------

func TestInitFlags(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	InitFlags(cmd)
	for _, name := range []string{"http", "direct", "disc-url", "disc-addr", "dmsgconf", "sess", "srv"} {
		require.NotNil(t, cmd.Flags().Lookup(name), "flag %q should be registered", name)
	}
}

func TestInitConfig(t *testing.T) {
	orig := DmsgHTTPPath
	defer func() { DmsgHTTPPath = orig }()

	// Empty path: nothing to load.
	DmsgHTTPPath = ""
	require.NoError(t, InitConfig())

	// Non-existent path: read error surfaces.
	DmsgHTTPPath = "/nonexistent/dmsghttp-config.json"
	require.Error(t, InitConfig())
}

func TestExecName(t *testing.T) {
	require.NotEmpty(t, ExecName())
}

func TestExecute(t *testing.T) {
	ran := false
	cmd := &cobra.Command{Use: "x", RunE: func(_ *cobra.Command, _ []string) error {
		ran = true
		return nil
	}}
	cmd.SetArgs([]string{})
	Execute(cmd) // success path must not call log.Fatal
	require.True(t, ran)
}

// ---- cachingDiscClient -----------------------------------------------------

func TestCachingDiscClient(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	synthetic := &disc.Entry{Static: pk}

	t.Run("synthetic entry short-circuits base", func(t *testing.T) {
		base := &fakeDisc{}
		c := newCachingDiscClient(base, synthetic, testLog())
		got, err := c.Entry(context.Background(), pk)
		require.NoError(t, err)
		require.Same(t, synthetic, got)
		require.Equal(t, 0, base.count("Entry"))
	})

	t.Run("non-matching PK delegates to base", func(t *testing.T) {
		base := &fakeDisc{entry: &disc.Entry{Static: other}}
		c := newCachingDiscClient(base, synthetic, testLog())
		_, err := c.Entry(context.Background(), other)
		require.NoError(t, err)
		require.Equal(t, 1, base.count("Entry"))
	})

	t.Run("nil synthetic delegates to base", func(t *testing.T) {
		base := &fakeDisc{entry: &disc.Entry{Static: pk}}
		c := newCachingDiscClient(base, nil, testLog())
		_, err := c.Entry(context.Background(), pk)
		require.NoError(t, err)
		require.Equal(t, 1, base.count("Entry"))
	})

	t.Run("all other methods delegate to base", func(t *testing.T) {
		base := &fakeDisc{}
		c := newCachingDiscClient(base, synthetic, testLog())
		ctx := context.Background()
		require.NoError(t, c.PostEntry(ctx, nil))
		require.NoError(t, c.PutEntry(ctx, cipher.SecKey{}, nil))
		require.NoError(t, c.DelEntry(ctx, nil))
		_, _ = c.AvailableServers(ctx)
		_, _ = c.AllServers(ctx)
		_, _ = c.AllEntries(ctx)
		_, _ = c.AllClientsByServer(ctx)
		_, _ = c.ClientsByServer(ctx, pk)
		for _, m := range []string{"PostEntry", "PutEntry", "DelEntry", "AvailableServers", "AllServers", "AllEntries", "AllClientsByServer", "ClientsByServer"} {
			require.Equal(t, 1, base.count(m), "base.%s", m)
		}
	})
}

// ---- fallbackDiscClient ----------------------------------------------------

func TestFallbackDiscClient_Entry(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	t.Run("direct hit returns without HTTP", func(t *testing.T) {
		direct := &fakeDisc{entry: &disc.Entry{Static: pk}}
		httpC := &fakeDisc{}
		f := newFallbackDiscClient(direct, httpC, testLog())
		got, err := f.Entry(context.Background(), pk)
		require.NoError(t, err)
		require.Equal(t, pk, got.Static)
		require.Equal(t, 1, direct.count("Entry"))
		require.Equal(t, 0, httpC.count("Entry"))
	})

	t.Run("direct miss falls back to HTTP", func(t *testing.T) {
		direct := &fakeDisc{entryErr: errors.New("not found")}
		httpC := &fakeDisc{entry: &disc.Entry{Static: pk}}
		f := newFallbackDiscClient(direct, httpC, testLog())
		got, err := f.Entry(context.Background(), pk)
		require.NoError(t, err)
		require.Equal(t, pk, got.Static)
		require.Equal(t, 1, direct.count("Entry"))
		require.Equal(t, 1, httpC.count("Entry"))
	})
}

func TestFallbackDiscClient_Delegation(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	direct := &fakeDisc{}
	httpC := &fakeDisc{}
	f := newFallbackDiscClient(direct, httpC, testLog())
	ctx := context.Background()

	require.NoError(t, f.PostEntry(ctx, nil))
	require.NoError(t, f.PutEntry(ctx, cipher.SecKey{}, nil))
	require.NoError(t, f.DelEntry(ctx, nil))
	_, _ = f.AvailableServers(ctx)
	_, _ = f.AllServers(ctx)
	_, _ = f.AllEntries(ctx)
	_, _ = f.AllClientsByServer(ctx)
	_, _ = f.ClientsByServer(ctx, pk)

	// Writes/reads that the direct client owns.
	require.Equal(t, 1, direct.count("PostEntry"))
	require.Equal(t, 1, direct.count("DelEntry"))
	require.Equal(t, 1, direct.count("AvailableServers"))
	require.Equal(t, 1, direct.count("AllServers"))
	require.Equal(t, 1, direct.count("AllEntries"))

	// Calls the HTTP client owns.
	require.Equal(t, 1, httpC.count("PutEntry"))
	require.Equal(t, 1, httpC.count("AllClientsByServer"))
	require.Equal(t, 1, httpC.count("ClientsByServer"))

	// And not the other way around.
	require.Equal(t, 0, httpC.count("PostEntry"))
	require.Equal(t, 0, direct.count("PutEntry"))
}

// ---- Start* paths: only the early-return branches that never construct a
// dmsg client (and therefore never start Serve / block on Close). The full
// connect paths are network/integration territory — see note at top.

func TestStart_NilLogger(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	ctx := context.Background()

	_, _, err := StartDmsg(ctx, nil, pk, sk, &http.Client{}, "http://disc", 1)
	require.ErrorContains(t, err, "nil logger")

	_, _, err = StartDmsgWithDirectClient(ctx, nil, pk, sk, 1)
	require.ErrorContains(t, err, "nil logger")

	_, _, err = StartDmsgWithSyntheticDiscovery(ctx, nil, pk, sk, &http.Client{}, "", 1)
	require.ErrorContains(t, err, "nil logger")
}

func TestStartDmsgDirectWithServers_EarlyErrors(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()

	t.Run("no servers", func(t *testing.T) {
		_, _, err := StartDmsgDirectWithServers(context.Background(), testLog(), pk, sk, "", nil, 1, "dest")
		require.ErrorContains(t, err, "no DMSG servers provided")
	})

	t.Run("invalid destination (returns before any client/dial)", func(t *testing.T) {
		spk, _ := cipher.GenerateKeyPair()
		srv := &disc.Entry{Static: spk, Server: &disc.Server{Address: "1.2.3.4:80"}}
		_, _, err := StartDmsgDirectWithServers(context.Background(), testLog(), pk, sk, "", []*disc.Entry{srv}, 1, "not-a-pk")
		require.ErrorContains(t, err, "destination address (pk) is wrong")
	})
}

func TestStartDmsgDirect_NoNetwork(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	// A non-empty, non-pk destination fails the pk parse inside
	// StartDmsgDirectWithServers before any client is built or dialed.
	// (If no Prod servers are configured it errors even earlier.)
	_, _, err := StartDmsgDirect(context.Background(), testLog(), pk, sk, "", 1, "not-a-pk")
	require.Error(t, err)
}

func TestInitDmsgWithFlags_InvalidServerAddr(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	orig := DmsgServerAddr
	defer func() { DmsgServerAddr = orig }()

	// An invalid --srv value fails ParseServerAddr before any client work.
	DmsgServerAddr = "no-at-sign"
	_, _, err := InitDmsgWithFlags(context.Background(), testLog(), pk, sk, &http.Client{}, "")
	require.Error(t, err)
}

// ---- FallbackRoundTripper --------------------------------------------------

func TestFallbackRoundTripper_NoClients(t *testing.T) {
	rt := NewFallbackRoundTripper(context.Background(), nil)

	t.Run("no body", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		require.NoError(t, err)
		_, err = rt.RoundTrip(req)
		require.Error(t, err)
		require.Contains(t, err.Error(), "all DMSG transports failed")
	})

	t.Run("with body (buffered for replay)", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, "http://example.com", strings.NewReader("payload"))
		require.NoError(t, err)
		_, err = rt.RoundTrip(req)
		require.Error(t, err)
		require.Contains(t, err.Error(), "all DMSG transports failed")
	})
}
