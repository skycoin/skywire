package app2

import (
	"fmt"

	"github.com/skycoin/skywire/pkg/app2/appserver"

	"github.com/skycoin/skycoin/src/util/logging"
)

type App struct {
	config Config
	log    *logging.Logger
	proc   *Proc
	rpcS   *appserver.Server
}

func New(log *logging.Logger, c Config, args []string) *App {
	p := NewProc(c, args)

	return &App{
		config: c,
		log:    log,
		proc:   p,
	}
}

func (a *App) PID() ProcID {
	return a.proc.ID()
}

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

func (a *App) Stop() error {
	a.closeRPCServer()

	return a.proc.Stop()
}

func (a *App) Wait() error {
	err := a.proc.Wait()
	a.closeRPCServer()
	return err
}

func (a *App) closeRPCServer() {
	if err := a.rpcS.Close(); err != nil {
		a.log.WithError(err).Error("error closing RPC server")
	}
}
