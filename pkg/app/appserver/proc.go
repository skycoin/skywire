package appserver

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appcommon"
)

// Proc is a wrapper for a skywire app. Encapsulates
// the running process itself and the RPC server for
// app/visor communication.
type Proc struct {
	key    appcommon.Key
	config appcommon.Config
	log    *logging.Logger
	cmd    *exec.Cmd
	doneC  chan struct{}
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
	env = append(env, fmt.Sprintf(sockFileEnvFormat, c.SockFilePath))
	env = append(env, fmt.Sprintf(visorPKEnvFormat, c.VisorPK))

	cmd := exec.Command(binaryPath, args...) // nolint:gosec

	cmd.Env = env
	cmd.Dir = c.WorkDir

	cmd.Stdout = stdout
	cmd.Stderr = stderr

	return &Proc{
		key:    key,
		config: c,
		log:    log,
		cmd:    cmd,
		doneC:  make(chan struct{}),
	}, nil
}

// Start starts the application.
func (p *Proc) Start() error {
	return p.cmd.Start()
}

// Stop stops the application.
func (p *Proc) Stop() error {
	err := p.cmd.Process.Signal(os.Interrupt)
	if err != nil {
		return err
	}

	<-p.doneC

	return nil
}

// Wait waits for the application cmd to exit.
func (p *Proc) Wait() error {
	err := p.cmd.Wait()
	close(p.doneC)
	return err
}

// getBinaryPath formats binary path using app dir, name and version.
func getBinaryPath(dir, name, ver string) string {
	const binaryNameFormat = "%s.v%s"
	return filepath.Join(dir, fmt.Sprintf(binaryNameFormat, name, ver))
}
