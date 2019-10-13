package appserver

import (
	"fmt"

	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/skywire/pkg/app2"
)

type App struct {
	config app2.Config
	log    *logging.Logger
	proc   *app2.Proc
	rpcS   *Server
}

func NewApp(log *logging.Logger, c app2.Config, dir string, args []string) (*App, error) {
	appKey := app2.GenerateAppKey()

	rpcS, err := New(logging.MustGetLogger(fmt.Sprintf("app_rpc_server_%s", appKey)), c.SockFile, appKey)
	if err != nil {
		return nil, err
	}

	p := app2.NewProc(c, dir, args)

	return &App{
		config: c,
		log:    log,
		proc:   p,
		rpcS:   rpcS,
	}, nil
}

func (a *App) PID() app2.ProcID {
	return a.proc.ID()
}

func (a *App) Run() error {
	go func() {
		if err := a.rpcS.ListenAndServe(); err != nil {
			a.log.WithError(err).Error("error serving RPC")
		}
	}()

	err := a.proc.Run()
	if err != nil {
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

	return a.proc.Wait()
}

func (a *App) closeRPCServer() {
	if err := a.rpcS.Close(); err != nil {
		a.log.WithError(err).Error("error closing RPC server")
	}
}
