// Package visor implements skywire visor.
package visor

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/launcher"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/dmsgpty"
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appserver"
	"github.com/SkycoinProject/skywire-mainnet/pkg/restart"
	"github.com/SkycoinProject/skywire-mainnet/pkg/router"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/transport"
	"github.com/SkycoinProject/skywire-mainnet/pkg/util/updater"
)

var (
	// ErrAppProcNotRunning represents lookup error for App related calls.
	ErrAppProcNotRunning = errors.New("no process of given app is running")
)

const (
	supportedProtocolVersion = "0.1.0"
	ownerRWX                 = 0700
	shortHashLen             = 6
)

// Visor provides messaging runtime for Apps by setting up all
// necessary connections and performing messaging gateway functions.
type Visor struct {
	reportCh   chan vReport
	closeStack []closeElem

	conf *Config
	log  *logging.Logger

	startedAt  time.Time
	restartCtx *restart.Context
	updater    *updater.Updater

	net    *snet.Network
	pty    *dmsgpty.Host
	tpM    *transport.Manager
	router router.Router

	procM appserver.ProcManager // proc manager
	appL  *launcher.Launcher    // app launcher
}

type vReport struct {
	src string
	err error
}

type reportFunc func(err error) bool

func (v *Visor) makeReporter(src string) reportFunc {
	return func(err error) bool {
		v.reportCh <- vReport{src: src, err: err}
		return err == nil
	}
}

func (v *Visor) processReports(log logrus.FieldLogger, ok *bool) {
	if log == nil {
		// nolint:ineffassign
		log = v.log
	}
	for {
		select {
		case report := <-v.reportCh:
			if report.err != nil {
				v.log.WithError(report.err).WithField("_src", report.src).Error()
				if ok != nil {
					*ok = false
				}
			}
		default:
			return
		}
	}
}

type closeElem struct {
	src string
	fn  func() bool
}

func (v *Visor) pushCloseStack(src string, fn func() bool) {
	v.closeStack = append(v.closeStack, closeElem{src: src, fn: fn})
}

// MasterLogger returns the underlying master logger (currently contained in visor config).
func (v *Visor) MasterLogger() *logging.MasterLogger {
	return v.conf.log
}

// NewVisor constructs new Visor.
func NewVisor(conf *Config, restartCtx *restart.Context) (v *Visor, ok bool) {
	ok = true

	v = &Visor{
		reportCh:   make(chan vReport, 100),
		log:        conf.log.PackageLogger("visor"),
		conf:       conf,
		restartCtx: restartCtx,
	}

	if lvl, err := logging.LevelFromString(conf.LogLevel); err == nil {
		v.conf.log.SetLevel(lvl)
	}

	log := v.MasterLogger().PackageLogger("visor:startup")
	log.WithField("public_key", conf.KeyPair.PubKey).Info("Begin startup.")
	v.startedAt = time.Now()

	for i, startFn := range initStack() {
		name := strings.ToLower(strings.TrimPrefix(filepath.Base(runtime.FuncForPC(reflect.ValueOf(startFn).Pointer()).Name()), "visor.init"))
		start := time.Now()

		log := v.MasterLogger().PackageLogger(fmt.Sprintf("visor:startup:%s", name)).
			WithField("func", fmt.Sprintf("[%d/%d]", i+1, len(initStack())))
		log.Info("Starting module...")

		if ok := startFn(v); !ok {
			log.WithField("elapsed", time.Since(start)).Error("Failed to start module.")
			v.processReports(log, nil)
			return v, ok
		}

		log.WithField("elapsed", time.Since(start)).Info("Module started successfully.")
	}

	if v.processReports(log, &ok); !ok {
		log.Error("Failed to startup visor.")
		return v, ok
	}

	log.Info("Startup complete!")
	return v, ok
}

// Close safely stops spawned Apps and Visor.
func (v *Visor) Close() error {
	if v == nil {
		return nil
	}

	log := v.MasterLogger().PackageLogger("visor:shutdown")
	log.Info("Begin shutdown.")

	for i, ce := range v.closeStack {

		start := time.Now()
		done := make(chan bool, 1)
		t := time.NewTimer(time.Second * 2)

		log := v.MasterLogger().PackageLogger(fmt.Sprintf("visor:shutdown:%s", ce.src)).
			WithField("func", fmt.Sprintf("[%d/%d]", i+1, len(v.closeStack)))
		log.Info("Shutting down module...")

		go func(ce closeElem) {
			done <- ce.fn()
			close(done)
		}(ce)

		select {
		case ok := <-done:
			if !ok {
				log.WithField("elapsed", time.Since(start)).Warn("Module stopped with unexpected result.")
				v.processReports(log, nil)
				continue
			}
			log.WithField("elapsed", time.Since(start)).Info("Module stopped cleanly.")
		case <-t.C:
			log.WithField("elapsed", time.Since(start)).Error("Module timed out.")
		}
	}

	v.processReports(v.log, nil)
	log.Info("Shutdown complete. Goodbye!")
	return nil
}

// TpDiscClient is a convenience function to obtain transport discovery client.
func (v *Visor) TpDiscClient() transport.DiscoveryClient {
	return v.tpM.Conf.DiscoveryClient
}

// Exec executes a shell command. It returns combined stdout and stderr output and an error.
func (v *Visor) Exec(command string) ([]byte, error) {
	args := strings.Split(command, " ")
	cmd := exec.Command(args[0], args[1:]...) // nolint: gosec
	return cmd.CombinedOutput()
}

// Update updates visor.
// It checks if visor update is available.
// If it is, the method downloads a new visor versions, starts it and kills the current process.
func (v *Visor) Update() (bool, error) {
	updated, err := v.updater.Update()
	if err != nil {
		v.log.Errorf("Failed to update visor: %v", err)
		return false, err
	}

	return updated, nil
}

// UpdateAvailable checks if visor update is available.
func (v *Visor) UpdateAvailable() (*updater.Version, error) {
	version, err := v.updater.UpdateAvailable()
	if err != nil {
		v.log.Errorf("Failed to check if visor update is available: %v", err)
		return nil, err
	}

	return version, nil
}

func (v *Visor) setAutoStart(appName string, autoStart bool) error {
	if _, ok := v.appL.AppState(appName); !ok {
		return ErrAppProcNotRunning
	}

	v.log.Infof("Saving auto start = %v for app %v to config", autoStart, appName)
	return v.conf.UpdateAppAutostart(v.appL, appName, autoStart)
}

func (v *Visor) setSocksPassword(password string) error {
	v.log.Infof("Changing skysocks password to %q", password)

	const (
		socksName       = "skysocks"
		passcodeArgName = "-passcode"
	)

	if err := v.conf.UpdateAppArg(v.appL, socksName, passcodeArgName, password); err != nil {
		return err
	}

	if _, ok := v.procM.ProcByName(socksName); ok {
		v.log.Infof("Updated %v password, restarting it", socksName)
		return v.appL.RestartApp(socksName)
	}

	v.log.Infof("Updated %v password", socksName)

	return nil
}

func (v *Visor) setSocksClientPK(pk cipher.PubKey) error {
	v.log.Infof("Changing skysocks-client PK to %q", pk)

	const (
		socksClientName = "skysocks-client"
		pkArgName       = "-srv"
	)

	if err := v.conf.UpdateAppArg(v.appL, socksClientName, pkArgName, pk.String()); err != nil {
		return err
	}

	if _, ok := v.procM.ProcByName(socksClientName); ok {
		v.log.Infof("Updated %v PK, restarting it", socksClientName)
		return v.appL.RestartApp(socksClientName)
	}

	v.log.Infof("Updated %v PK", socksClientName)

	return nil
}

// UnlinkSocketFiles removes unix socketFiles from file system
func UnlinkSocketFiles(socketFiles ...string) error {
	for _, f := range socketFiles {
		if err := syscall.Unlink(f); err != nil {
			if !strings.Contains(err.Error(), "no such file or directory") {
				return err
			}
		}
	}

	return nil
}
