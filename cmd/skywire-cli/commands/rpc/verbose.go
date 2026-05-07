// Package clirpc cmd/skywire-cli/commands/rpc/verbose.go
//
// Verbose log-streaming helper for cli commands that block on a
// visor-side RPC and want to surface what the visor is doing in
// real time.
//
// Two APIs:
//
//   - OpenVerbose returns a *VerboseStream the caller drives by hand.
//     Use this when the calling command needs to interleave the
//     stream with its own RPCs (proxy start / vpn start: subscribe,
//     start app, poll for Running, wait on ctx.Done, stop app).
//
//   - WithVerbose wraps a single bounded operation. Use this for the
//     RPC-and-done shape (dmsg curl, skynet curl, tp add, dmsg probe).
//     Callers in this form don't need to know the stream exists
//     when --verbose is off — the helper short-circuits.
//
// Both wait for a subscribe-handshake sentinel before the wrapped
// action runs so the first ~50ms of visor activity isn't lost to the
// stream-handshake race.
package clirpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
)

// VerboseFilter is the per-command filter spec passed to the
// gRPC StreamAppLogs request.
type VerboseFilter struct {
	// AppName matches by _module exact equality OR app/app_name
	// field. Empty when scoping by Modules only (e.g. dmsg curl).
	AppName string
	// Modules is a prefix list matched against the _module field.
	// OR-merged with AppName.
	Modules []string
	// Level is the minimum severity ("trace"|"debug"|"info"|"warn"|"error").
	// Empty defaults to "debug".
	Level string
}

// VerboseStream is a live subscription to the visor's master logger.
// Entries are formatted and written to stderr by a goroutine until
// ctx is canceled or Close is called.
type VerboseStream struct {
	grpc       *rpcgrpc.PingClient
	subscribed chan struct{}
	done       chan struct{}
}

// OpenVerbose dials the visor's gRPC endpoint and starts streaming
// matching entries to stderr. Returns immediately; caller calls
// WaitSubscribed before the action they want to observe, and Close
// when done.
//
// Either filter.AppName or filter.Modules must be non-empty.
//
// rpcAddr is typically clirpc.Addr; the gRPC server is multiplexed
// onto the same port as the standard RPC server via cmux.
func OpenVerbose(ctx context.Context, rpcAddr string, filter VerboseFilter) (*VerboseStream, error) {
	if filter.AppName == "" && len(filter.Modules) == 0 {
		return nil, errors.New("verbose: filter requires AppName or Modules")
	}
	grpcClient, err := rpcgrpc.NewPingClient(rpcAddr)
	if err != nil {
		return nil, fmt.Errorf("verbose: gRPC dial failed: %w", err)
	}
	level := filter.Level
	if level == "" {
		level = "debug"
	}

	v := &VerboseStream{
		grpc:       grpcClient,
		subscribed: make(chan struct{}),
		done:       make(chan struct{}),
	}
	go func() {
		defer close(v.done)
		defer grpcClient.Close() //nolint:errcheck,gosec
		err := grpcClient.StreamAppLogs(ctx, filter.AppName, false, level, filter.Modules, v.subscribed, func(e *rpcgrpc.AppLogEntry) {
			emitEntry(os.Stderr, e)
		})
		if err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "verbose stream ended: %v\n", err)
		}
	}()
	return v, nil
}

// WaitSubscribed blocks until the server confirms the subscription
// is live (the Subscribed sentinel was received) or timeout fires.
// Returns context.DeadlineExceeded on timeout — callers usually
// proceed anyway, since the stream may still attach mid-action.
func (v *VerboseStream) WaitSubscribed(ctx context.Context, timeout time.Duration) error {
	select {
	case <-v.subscribed:
		return nil
	case <-time.After(timeout):
		fmt.Fprintln(os.Stderr, "verbose: subscription not confirmed within timeout; proceeding anyway")
		return context.DeadlineExceeded
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close waits for the stream goroutine to exit, tearing down the
// underlying gRPC client. Safe to call once. Caller is responsible
// for canceling ctx (or letting it cancel naturally) so the stream
// goroutine returns.
func (v *VerboseStream) Close() {
	<-v.done
}

// WithVerbose is the function-wrapper convenience. If filter is the
// zero value (empty AppName + empty Modules), it is a no-op and just
// runs fn. Otherwise it opens the stream, waits up to 2s for the
// subscribe handshake, runs fn, and waits for the stream goroutine
// to exit (ctx-canceled by the caller or naturally drained).
//
// Returns whatever fn returned.
func WithVerbose(ctx context.Context, rpcAddr string, filter VerboseFilter, fn func() error) error {
	if filter.AppName == "" && len(filter.Modules) == 0 {
		return fn()
	}
	v, err := OpenVerbose(ctx, rpcAddr, filter)
	if err != nil {
		return err
	}
	defer v.Close()
	_ = v.WaitSubscribed(ctx, 2*time.Second) //nolint:errcheck // best-effort
	return fn()
}

// verboseFormatter mirrors the visor's MasterLogger text formatter so
// streamed entries render identically to entries written locally on
// the visor. ForceFormatting=true keeps the output stable when stderr
// isn't a terminal (e.g. piped to a file); colors follow the terminal
// detection in formatter.init.
var verboseFormatter = &logging.TextFormatter{
	FullTimestamp:      true,
	AlwaysQuoteStrings: true,
	QuoteEmptyFields:   true,
	ForceFormatting:    true,
	TimestampFormat:    "2006-01-02T15:04:05.0000Z07:00",
}

// emitEntry writes one log line to w in the visor's MasterLogger
// format so cli `--verbose` output is byte-identical to what the
// visor would print locally for the same entry: bracketed timestamp,
// level, [module]: prefix, message, then key=value fields.
func emitEntry(w io.Writer, e *rpcgrpc.AppLogEntry) {
	level, err := logging.LevelFromString(e.Level)
	if err != nil {
		level = logrus.DebugLevel
	}
	data := logrus.Fields{}
	if e.Module != "" {
		data["_module"] = e.Module
	}
	for k, v := range e.GetFields() {
		data[k] = v
	}
	entry := &logrus.Entry{
		Time:    time.Unix(0, e.TimestampNs),
		Level:   level,
		Message: e.Message,
		Data:    data,
	}
	b, fErr := verboseFormatter.Format(entry)
	if fErr != nil {
		// Should be unreachable; fall back to a plain line so
		// the user still sees something on a formatter glitch.
		fmt.Fprintf(w, "[%s] %s [%s]: %s\n", entry.Time.Format(time.RFC3339Nano), e.Level, e.Module, e.Message) //nolint:errcheck,gosec
		return
	}
	_, _ = w.Write(b) //nolint:errcheck
}
