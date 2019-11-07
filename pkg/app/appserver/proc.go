package appserver

import (
	"fmt"
	"io"
	"os/exec"
	"path/filepath"

	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appcommon"
)

// Proc is a wrapper for a skywire app. Encapsulates
// the running proccess itself and the RPC server for
// app/visor communication.
type Proc struct {
	key    appcommon.Key
	config appcommon.Config
	log    *logging.Logger
	rpcS   *Server
	cmd    *exec.Cmd
}

// NewProc constructs `Proc`.
func NewProc(log *logging.Logger, c appcommon.Config, args []string, stdout, stderr io.Writer) (*Proc, error) {
	key := appcommon.GenerateAppKey()

	binaryPath := getBinaryPath(c.BinaryDir, c.Name, c.Version)

	const (
		appKeyEnvFormat   = appcommon.EnvAppKey + "=%s"
		sockFileEnvFormat = appcommon.EnvSockFile + "=%s"
		visorPKEnvFormat  = appcommon.EnvVisorPK + "=%s"
	)

	env := make([]string, 0, 3)
	env = append(env, fmt.Sprintf(appKeyEnvFormat, key))
	env = append(env, fmt.Sprintf(sockFileEnvFormat, c.SockFile))
	env = append(env, fmt.Sprintf(visorPKEnvFormat, c.VisorPK))

	cmd := exec.Command(binaryPath, args...) // nolint:gosec

	cmd.Env = env
	cmd.Dir = c.WorkDir

	cmd.Stdout = stdout
	cmd.Stderr = stderr

	rpcS, err := New(logging.MustGetLogger(fmt.Sprintf("app_rpc_server_%s", key)),
		c.SockFile, key)
	if err != nil {
		return nil, err
	}

	return &Proc{
		key:    key,
		config: c,
		log:    log,
		cmd:    cmd,
		rpcS:   rpcS,
	}, nil
}

// Run runs the application. It starts the process and runs the
// RPC communication server.
func (p *Proc) Run() error {
	go func() {
		if err := p.rpcS.ListenAndServe(); err != nil {
			p.log.WithError(err).Error("error serving RPC")
		}
	}()

	if err := p.cmd.Run(); err != nil {
		p.closeRPCServer()
		return err
	}

	return nil
}

// Stop stops the applicacation. It stops the process and
// shuts down the RPC server.
func (p *Proc) Stop() error {
	p.closeRPCServer()
	return p.cmd.Process.Kill()
}

// Wait shuts down the RPC server and waits for the
// application cmd to exit.
func (p *Proc) Wait() error {
	p.closeRPCServer()
	return p.cmd.Wait()
}

// closeRPCServer closes RPC server and logs error if any.
func (p *Proc) closeRPCServer() {
	if err := p.rpcS.Close(); err != nil {
		p.log.WithError(err).Error("error closing RPC server")
	}
}

// getBinaryPath formats binary path using app dir, name and version.
func getBinaryPath(dir, name, ver string) string {
	const binaryNameFormat = "%s.v%s"
	return filepath.Join(dir, fmt.Sprintf(binaryNameFormat, name, ver))
}
