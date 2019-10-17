package app2

import (
	"fmt"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app2/appserver"
)

// App defines a skywire application. It encapsulates
// running process and the RPC server to communicate with
// the app.
type App struct {
	key    Key
	config Config
	log    *logging.Logger
	proc   *Proc
	rpcS   *appserver.Server
}

// New constructs `App`.
func New(log *logging.Logger, c Config, args []string) *App {
	k := GenerateAppKey()

	p := NewProc(c, args, k)

	return &App{
		key:    k,
		config: c,
		log:    log,
		proc:   p,
	}
}

// PID returns app PID.
func (a *App) PID() ProcID {
	return a.proc.ID()
}

// Run sets the app running. It starts app process
// and sets up the RPC server.
func (a *App) Run() error {
	appKey := GenerateAppKey()

	rpcS, err := appserver.New(logging.MustGetLogger(fmt.Sprintf("app_rpc_server_%s", appKey)),
		a.config.SockFile, appKey)
	if err != nil {
		return err
	}

	a.rpcS = rpcS

	go func() {
		if err := a.rpcS.ListenAndServe(); err != nil {
			a.log.WithError(err).Error("error serving RPC")
		}
	}()

	if err := a.proc.Run(); err != nil {
		a.closeRPCServer()
		return err
	}

	return nil
}

// Stop stops application (process and the RPC server).
func (a *App) Stop() error {
	a.closeRPCServer()

	return a.proc.Stop()
}

// Wait waits for the app to exit. Shuts down the
// RPC server.
func (a *App) Wait() error {
	err := a.proc.Wait()
	a.closeRPCServer()
	return err
}

// closeRPCServer closes RPC server and logs error if any.
func (a *App) closeRPCServer() {
	if err := a.rpcS.Close(); err != nil {
		a.log.WithError(err).Error("error closing RPC server")
	}
}
