// Package services services_test.go: unit tests for the three top-level
// files — the Duration JSON wrapper (duration.go), the block/registry
// primitives (services.go), and the file parsing + supervisor (run.go).
package services

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// ---- duration.go -----------------------------------------------------------

func TestDuration_MarshalJSON(t *testing.T) {
	b, err := Duration(2 * time.Minute).MarshalJSON()
	require.NoError(t, err)
	require.Equal(t, `"2m0s"`, string(b))
}

func TestDuration_UnmarshalJSON(t *testing.T) {
	t.Run("from string", func(t *testing.T) {
		var d Duration
		require.NoError(t, d.UnmarshalJSON([]byte(`"30s"`)))
		require.Equal(t, 30*time.Second, d.Std())
	})

	t.Run("from number (nanoseconds)", func(t *testing.T) {
		var d Duration
		require.NoError(t, d.UnmarshalJSON([]byte(`1000000000`)))
		require.Equal(t, time.Second, d.Std())
	})

	t.Run("empty bytes -> zero", func(t *testing.T) {
		var d Duration
		require.NoError(t, d.UnmarshalJSON(nil))
		require.Equal(t, time.Duration(0), d.Std())
	})

	t.Run("invalid duration string", func(t *testing.T) {
		var d Duration
		require.Error(t, d.UnmarshalJSON([]byte(`"not-a-duration"`)))
	})

	t.Run("invalid JSON", func(t *testing.T) {
		var d Duration
		require.Error(t, d.UnmarshalJSON([]byte(`{bad`)))
	})

	t.Run("wrong type -> invalid duration", func(t *testing.T) {
		var d Duration
		err := d.UnmarshalJSON([]byte(`true`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid duration")
	})
}

func TestDuration_RoundTripInStruct(t *testing.T) {
	type cfg struct {
		Timeout Duration `json:"timeout"`
	}
	out, err := json.Marshal(cfg{Timeout: Duration(90 * time.Second)})
	require.NoError(t, err)
	require.Contains(t, string(out), `"1m30s"`)

	var back cfg
	require.NoError(t, json.Unmarshal(out, &back))
	require.Equal(t, 90*time.Second, back.Timeout.Std())
}

// ---- services.go: Block ----------------------------------------------------

func TestBlock_UnmarshalJSON(t *testing.T) {
	t.Run("valid with name", func(t *testing.T) {
		var b Block
		require.NoError(t, b.UnmarshalJSON([]byte(`{"type":"dmsg-discovery","name":"dd1","addr":":9090"}`)))
		require.Equal(t, "dmsg-discovery", b.Type)
		require.Equal(t, "dd1", b.Name)
		// Raw retains the full block so the factory can re-decode.
		require.JSONEq(t, `{"type":"dmsg-discovery","name":"dd1","addr":":9090"}`, string(b.Raw))
	})

	t.Run("missing type", func(t *testing.T) {
		var b Block
		err := b.UnmarshalJSON([]byte(`{"name":"x"}`))
		require.Error(t, err)
		require.Contains(t, err.Error(), `missing required "type"`)
	})

	t.Run("malformed json", func(t *testing.T) {
		var b Block
		require.Error(t, b.UnmarshalJSON([]byte(`{bad`)))
	})
}

func TestBlock_Label(t *testing.T) {
	require.Equal(t, "myname", (&Block{Type: "t", Name: "myname"}).Label())
	require.Equal(t, "t", (&Block{Type: "t"}).Label()) // falls back to type
}

// ---- services.go: registry -------------------------------------------------

func nopFactory(_ json.RawMessage, _ *logging.Logger) (Service, error) { return nil, nil }

func TestRegisterLookup(t *testing.T) {
	const typ = "test-register-lookup"
	Register(typ, nopFactory)

	f, ok := Lookup(typ)
	require.True(t, ok)
	require.NotNil(t, f)

	_, ok = Lookup("definitely-not-registered-xyz")
	require.False(t, ok)
}

func TestRegister_DuplicatePanics(t *testing.T) {
	const typ = "test-register-dup"
	Register(typ, nopFactory)
	require.Panics(t, func() { Register(typ, nopFactory) })
}

func TestRegisteredTypes_SortedAndPresent(t *testing.T) {
	Register("zzz-rt-2", nopFactory)
	Register("zzz-rt-1", nopFactory)

	types := RegisteredTypes()
	require.Contains(t, types, "zzz-rt-1")
	require.Contains(t, types, "zzz-rt-2")

	// Output is globally sorted ascending.
	for i := 1; i < len(types); i++ {
		require.LessOrEqual(t, types[i-1], types[i])
	}
}

func TestSortStrings(t *testing.T) {
	s := []string{"c", "a", "b", "a"}
	sortStrings(s)
	require.Equal(t, []string{"a", "a", "b", "c"}, s)

	require.NotPanics(t, func() { sortStrings(nil) })
	single := []string{"x"}
	sortStrings(single)
	require.Equal(t, []string{"x"}, single)
}

// ---- run.go: ParseFile / LoadFile ------------------------------------------

func TestParseFile(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		f, err := ParseFile([]byte(`{"services":[{"type":"a"},{"type":"b","name":"b1"}]}`))
		require.NoError(t, err)
		require.Len(t, f.Services, 2)
		require.Equal(t, "a", f.Services[0].Type)
		require.Equal(t, "b1", f.Services[1].Label())
	})

	t.Run("malformed json", func(t *testing.T) {
		_, err := ParseFile([]byte(`{bad`))
		require.Error(t, err)
	})

	t.Run("no services", func(t *testing.T) {
		_, err := ParseFile([]byte(`{"services":[]}`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "no services")
	})
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("valid", func(t *testing.T) {
		p := filepath.Join(dir, "ok.json")
		require.NoError(t, os.WriteFile(p, []byte(`{"services":[{"type":"a"}]}`), 0o600))
		f, err := LoadFile(p)
		require.NoError(t, err)
		require.Len(t, f.Services, 1)
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := LoadFile(filepath.Join(dir, "nope.json"))
		require.Error(t, err)
	})
}

// ---- run.go: Run -----------------------------------------------------------

// stubService runs the supplied function; used to drive Run's success and
// error paths deterministically without a real deployment service.
type stubService struct {
	run func(ctx context.Context) error
}

func (s stubService) Run(ctx context.Context) error { return s.run(ctx) }

func testMaster() *logging.MasterLogger { return logging.NewMasterLogger() }

func TestRun_UnknownType(t *testing.T) {
	f, err := ParseFile([]byte(`{"services":[{"type":"run-unknown-type-zzz"}]}`))
	require.NoError(t, err)

	err = Run(context.Background(), f, testMaster())
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown type")
}

func TestRun_FactoryError(t *testing.T) {
	Register("run-factory-error", func(_ json.RawMessage, _ *logging.Logger) (Service, error) {
		return nil, errors.New("bad config")
	})
	f, err := ParseFile([]byte(`{"services":[{"type":"run-factory-error"}]}`))
	require.NoError(t, err)

	err = Run(context.Background(), f, testMaster())
	require.Error(t, err)
	require.Contains(t, err.Error(), "build")
	require.Contains(t, err.Error(), "bad config")
}

func TestRun_ContextCanceledReturnsNil(t *testing.T) {
	Register("run-ctx-canceled", func(_ json.RawMessage, _ *logging.Logger) (Service, error) {
		return stubService{run: func(ctx context.Context) error {
			<-ctx.Done()
			return nil
		}}, nil
	})
	f, err := ParseFile([]byte(`{"services":[{"type":"run-ctx-canceled"}]}`))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // service unwinds immediately

	require.NoError(t, Run(ctx, f, testMaster()))
}

func TestRun_ServiceErrorReturnedFirst(t *testing.T) {
	Register("run-service-error", func(_ json.RawMessage, _ *logging.Logger) (Service, error) {
		return stubService{run: func(_ context.Context) error {
			return errors.New("listener failed")
		}}, nil
	})
	f, err := ParseFile([]byte(`{"services":[{"type":"run-service-error","name":"svcX"}]}`))
	require.NoError(t, err)

	err = Run(context.Background(), f, testMaster())
	require.Error(t, err)
	require.Contains(t, err.Error(), "svcX")
	require.Contains(t, err.Error(), "listener failed")
}

func TestRun_ContextCanceledErrorIgnored(t *testing.T) {
	// A service returning context.Canceled is treated as a clean shutdown,
	// not a fatal error, so Run returns nil.
	Register("run-ctx-canceled-err", func(_ json.RawMessage, _ *logging.Logger) (Service, error) {
		return stubService{run: func(_ context.Context) error {
			return context.Canceled
		}}, nil
	})
	f, err := ParseFile([]byte(`{"services":[{"type":"run-ctx-canceled-err"}]}`))
	require.NoError(t, err)

	require.NoError(t, Run(context.Background(), f, testMaster()))
}
