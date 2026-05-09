// Package services pkg/services/run.go
//
// errgroup-style supervisor for a parsed services.File. Brought into
// its own file so the registry primitives in services.go stay small
// and easy to read.
package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/skycoin/skywire/pkg/logging"
)

// LoadFile reads a services.json from disk and returns the parsed
// File. Validates only that every block has a "type" — type-specific
// validation happens later when the Factory decodes the block.
func LoadFile(path string) (File, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return File{}, fmt.Errorf("services: read %s: %w", path, err)
	}
	return ParseFile(data)
}

// ParseFile parses the JSON bytes into a File. Exposed alongside
// LoadFile so callers (tests, embedded configs) can pass bytes
// directly without going through the filesystem.
func ParseFile(data []byte) (File, error) {
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("services: parse: %w", err)
	}
	if len(f.Services) == 0 {
		return File{}, errors.New("services: config has no services")
	}
	return f, nil
}

// Run dispatches every block in file to its registered Factory and
// supervises the resulting Services. Returns when:
//   - ctx is canceled (returns nil)
//   - SIGINT/SIGTERM is received (returns nil; ctx is canceled
//     internally so all services unwind cleanly)
//   - any service's Run returns an error (returns the first error;
//     other services are then canceled and joined)
//
// Each service runs in its own goroutine and shares the same ctx.
// Services log under their Block.Label() so an operator looking at
// merged stdout can tell two instances of the same service type
// apart.
func Run(ctx context.Context, file File, master *logging.MasterLogger) error {
	log := master.PackageLogger("services-supervisor")
	// Build all services up front before starting any. A factory
	// failure (bad config, bad PK, bad URL) is fatal — abort
	// without partial startup. Cheaper than discovering it 30s in
	// when the third service fails to bind.
	type built struct {
		block Block
		svc   Service
	}
	svcs := make([]built, 0, len(file.Services))
	for i, b := range file.Services {
		factory, ok := Lookup(b.Type)
		if !ok {
			return fmt.Errorf("services: block #%d (%s): unknown type %q (registered: %v)",
				i, b.Label(), b.Type, RegisteredTypes())
		}
		s, err := factory(b.Raw, master.PackageLogger(b.Label()))
		if err != nil {
			return fmt.Errorf("services: block #%d (%s): build: %w", i, b.Label(), err)
		}
		svcs = append(svcs, built{block: b, svc: s})
	}

	// Shared context: any service erroring cancels everyone, and
	// SIGINT/SIGTERM also cancel everyone. The signal goroutine is
	// joined via the wait group below so the process doesn't exit
	// before signal handling unwinds.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case s := <-sigCh:
			log.WithField("signal", s).Info("received shutdown signal; canceling all services")
			cancel()
		case <-runCtx.Done():
		}
	}()

	// First-error wins. Subsequent errors are logged but don't
	// overwrite — gives the operator the clearest signal of what
	// actually broke first vs. cascading collateral damage.
	var (
		firstErrMu sync.Mutex
		firstErr   error
		wg         sync.WaitGroup
	)
	recordErr := func(label string, err error) {
		if err == nil || errors.Is(err, context.Canceled) {
			return
		}
		firstErrMu.Lock()
		defer firstErrMu.Unlock()
		if firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", label, err)
			cancel()
			return
		}
		log.WithError(err).WithField("service", label).
			Warn("service errored after first fatal; ignoring")
	}

	for _, s := range svcs {
		wg.Add(1)
		go func(s built) {
			defer wg.Done()
			label := s.block.Label()
			log.WithField("service", label).WithField("type", s.block.Type).
				Info("starting service")
			err := s.svc.Run(runCtx)
			log.WithField("service", label).
				WithError(err).Info("service exited")
			recordErr(label, err)
		}(s)
	}
	wg.Wait()
	return firstErr
}
