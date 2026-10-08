package visor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var ownKeyRuns atomic.Int32

type flakyOwnKeyService struct{}

// Run fails on its first start and then runs until ctx ends.
func (flakyOwnKeyService) Run(ctx context.Context) error {
	if ownKeyRuns.Add(1) == 1 {
		return errors.New("first start fails")
	}
	<-ctx.Done()
	return nil
}

func init() {
	services.Register("test-own-key", func(json.RawMessage, *logging.Logger) (services.Service, error) {
		return flakyOwnKeyService{}, nil
	})
}

// A block with a key of its own runs as its own service, not mounted, under
// that key, and is started again after it stops.
func TestEmbeddedServiceRunsUnderItsOwnKey(t *testing.T) {
	ownKeyMinBackoff = 10 * time.Millisecond
	t.Cleanup(func() { ownKeyMinBackoff = 5 * time.Second })

	visorPK, visorSK := cipher.GenerateKeyPair()
	svcPK, svcSK := cipher.GenerateKeyPair()
	raw, err := json.Marshal(map[string]string{"type": "test-own-key", "secret_key": svcSK.Hex()})
	require.NoError(t, err)
	var b services.Block
	require.NoError(t, json.Unmarshal(raw, &b))

	common := &visorconfig.Common{PK: visorPK, SK: visorSK}
	common.SetLogger(logging.NewMasterLogger())
	v := &Visor{conf: &visorconfig.V1{Common: common, EmbeddedServices: []services.Block{b}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, initOwnKeyServices(ctx, v, logging.MustGetLogger("test")), "it needs no other module")
	require.NoError(t, initEmbeddedServices(ctx, v, logging.MustGetLogger("test")), "no dmsg HTTP mux is needed")
	require.NoError(t, initOwnKeyServices(ctx, v, logging.MustGetLogger("test")), "a resume starts no second runner")

	require.Eventually(t, func() bool {
		st := v.embeddedServiceStates()
		return len(st) == 1 && st[0].Running && st[0].Restarts == 1
	}, 5*time.Second, 10*time.Millisecond)
	st := v.embeddedServiceStates()[0]
	require.True(t, st.OwnKey)
	require.Equal(t, "dmsg://"+svcPK.Hex(), st.URL)
}

// An operator can stop, start and restart an own-key service on its own, and
// a mounted one is refused.
func TestEmbeddedServiceControl(t *testing.T) {
	ownKeyMinBackoff = 10 * time.Millisecond
	t.Cleanup(func() { ownKeyMinBackoff = 5 * time.Second })
	ownKeyRuns.Store(1) // every run below blocks until stopped

	visorPK, visorSK := cipher.GenerateKeyPair()
	_, svcSK := cipher.GenerateKeyPair()
	raw, err := json.Marshal(map[string]string{"type": "test-own-key", "name": "svc", "secret_key": svcSK.Hex()})
	require.NoError(t, err)
	var b services.Block
	require.NoError(t, json.Unmarshal(raw, &b))
	common := &visorconfig.Common{PK: visorPK, SK: visorSK}
	common.SetLogger(logging.NewMasterLogger())
	v := &Visor{conf: &visorconfig.V1{Common: common, EmbeddedServices: []services.Block{b}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, initOwnKeyServices(ctx, v, logging.MustGetLogger("test")))

	state := func() (bool, bool) {
		s := v.embeddedServiceStates()[0]
		return s.Running, s.Stopped
	}
	require.Eventually(t, func() bool { r, _ := state(); return r }, 5*time.Second, 10*time.Millisecond)

	require.NoError(t, v.EmbeddedServiceControl("svc", "stop"))
	require.Eventually(t, func() bool { r, s := state(); return !r && s }, 5*time.Second, 10*time.Millisecond)

	require.NoError(t, v.EmbeddedServiceControl("svc", "start"))
	require.Eventually(t, func() bool { r, s := state(); return r && !s }, 5*time.Second, 10*time.Millisecond)

	runs := ownKeyRuns.Load()
	require.NoError(t, v.EmbeddedServiceControl("svc", "restart"))
	require.Eventually(t, func() bool { r, _ := state(); return r && ownKeyRuns.Load() > runs }, 5*time.Second, 10*time.Millisecond)

	require.Error(t, v.EmbeddedServiceControl("svc", "pause"))
	require.Error(t, v.EmbeddedServiceControl("nope", "stop"))
}

// A block that cannot be mounted and names no key, such as stun, or keeps its
// key in its own config file, runs on its own too.
func TestEmbeddedServiceRunsStandaloneWithoutKey(t *testing.T) {
	ownKeyRuns.Store(1)
	filePK, _ := cipher.GenerateKeyPair()
	path := filepath.Join(t.TempDir(), "svc.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"public_key":"`+filePK.Hex()+`"}`), 0o600))

	var blocks []services.Block
	for _, raw := range []string{
		`{"type":"test-own-key","name":"keyless"}`,
		`{"type":"test-own-key","name":"filekey","config_path":"` + path + `"}`,
	} {
		var b services.Block
		require.NoError(t, json.Unmarshal([]byte(raw), &b))
		blocks = append(blocks, b)
	}
	visorPK, visorSK := cipher.GenerateKeyPair()
	common := &visorconfig.Common{PK: visorPK, SK: visorSK}
	common.SetLogger(logging.NewMasterLogger())
	v := &Visor{conf: &visorconfig.V1{Common: common, EmbeddedServices: blocks}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, initOwnKeyServices(ctx, v, logging.MustGetLogger("test")))
	require.NoError(t, initEmbeddedServices(ctx, v, logging.MustGetLogger("test")))

	require.Eventually(t, func() bool {
		st := v.embeddedServiceStates()
		return len(st) == 2 && st[0].Running && st[1].Running
	}, 5*time.Second, 10*time.Millisecond)
	st := v.embeddedServiceStates()
	require.Empty(t, st[0].URL)
	require.Equal(t, "dmsg://"+filePK.Hex(), st[1].URL)
	require.NoError(t, v.EmbeddedServiceControl("keyless", "stop"))
}
