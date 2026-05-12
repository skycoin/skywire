// Package commands cmd/skywire/commands/autoconfig.go
//
// `skywire autoconfig` is the single entry point operators use to
// (re)generate the visor config and (re)start the service. The shape
// of the output — paths, owner, systemd unit — is driven entirely by
// the SKYENV file (default /etc/skywire.conf, with one level of
// SKYENV= redirect honored by pkg/cmdutil/skyenv).
//
// Identity model:
//
//	PKGENV=true  → /opt/skywire/* paths, system-level systemd unit.
//	               When SKYWIRE_USER=name is also set in the file,
//	               autoconfig writes a drop-in pinning User= and
//	               chowns /opt/skywire/* to that user, so the visor
//	               runs as them.
//
//	USRENV=true  → $HOME-based paths, user-level systemd unit
//	               (`systemctl --user`).
//
//	neither set  → euid==0 falls back to PKGENV behavior, otherwise
//	               USRENV. This preserves the legacy "root install"
//	               flow for operators who haven't migrated.
//
// The only env var autoconfig itself reads is SKYENV (the path to
// the env file). Everything else lives inside that file.
package commands

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Color codes for terminal output
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[0;31m"
	colorGreen  = "\033[0;32m"
	colorYellow = "\033[0;33m"
	colorBlue   = "\033[1;34m"
	colorPurple = "\033[0;35m"
	colorCyan   = "\033[0;36m"
	colorBold   = "\033[1m"
)

// systemdDropIn is where autoconfig writes a per-install User= /
// Group= override. systemd merges *.conf files in this directory
// over the package-shipped unit; touching the unit file directly
// would be clobbered by the next package upgrade.
const systemdDropIn = "/etc/systemd/system/skywire.service.d/skywire-user.conf"

var autoconfigVerbose bool

func init() {
	autoconfigCmd.Flags().BoolVarP(&autoconfigVerbose, "verbose", "v", false, "show reward address, support links, and other details")
	RootCmd.AddCommand(autoconfigCmd)
}

// resolvedConfig captures the answers autoconfig derives from the
// skyenv file once, so every step downstream agrees on which paths
// and which systemd unit to operate on.
type resolvedConfig struct {
	skyenvPath  string // resolved env file path (after SKYENV= redirect)
	usrEnv      bool
	pkgEnv      bool
	skywireUser string // owner for PKGENV paths; empty = no chown
	configPath  string // resolved skywire.json path
	useUserUnit bool   // true = `systemctl --user`, false = system unit
}

var autoconfigCmd = &cobra.Command{
	Use:   "autoconfig",
	Short: "Automatic visor configuration for packages",
	Long: `Automatic visor configuration. Reads /etc/skywire.conf (or whatever
SKYENV points at), generates the visor config, manages the systemd
drop-in, and restarts (or prompts to start) the service.

Mode is selected by PKGENV/USRENV in the env file:

  PKGENV=true            → system install at /opt/skywire,
                           system-level systemd unit
  PKGENV=true
  SKYWIRE_USER=_skywire  → same, but visor runs as _skywire (drop-in
                           writes User=_skywire, /opt/skywire chowned)

  USRENV=true            → user install at $HOME, systemctl --user

  neither set            → falls back to PKGENV when run as root,
                           USRENV otherwise.`,
	Hidden: true,
	Run: func(_ *cobra.Command, args []string) {
		if os.Getenv("NOAUTOCONFIG") == "true" {
			fmt.Println("autoconfiguration disabled. to configure and start skywire run: skywire autoconfig")
			os.Exit(0)
		}

		msg2("Configuring skywire")

		versionOut, err := exec.Command("skywire", "-v").Output()
		if err == nil {
			version := strings.TrimSpace(string(versionOut))
			if !strings.Contains(version, "unknown") {
				msg2(fmt.Sprintf("version: %s", strings.Fields(version)[len(strings.Fields(version))-1]))
			}
		}

		hvArg := ""
		if len(args) > 0 {
			hvArg = args[0]
		}

		resolved := resolveConfig()

		if err := generateConfig(resolved, hvArg); err != nil {
			fmt.Printf("%s>>> FATAL:%s %v\n", colorRed, colorReset, err)
			os.Exit(1)
		}

		if _, err := os.Stat(resolved.configPath); os.IsNotExist(err) {
			mode := "PKGENV"
			if resolved.usrEnv {
				mode = "USRENV"
			}
			fmt.Printf("%s>>> FATAL:%s expected config file not found at %s\n", colorRed, colorReset, resolved.configPath)
			fmt.Printf("            autoconfig resolved mode=%s from %s\n", mode, func() string {
				if resolved.skyenvPath != "" {
					return resolved.skyenvPath
				}
				return "implicit fallback (no /etc/skywire.conf)"
			}())
			fmt.Printf("            if %s is commented out in %s, uncomment it and re-run `skywire autoconfig`\n",
				mode, func() string {
					if resolved.skyenvPath != "" {
						return resolved.skyenvPath
					}
					return "/etc/skywire.conf"
				}())
			os.Exit(100)
		}
		msg3(fmt.Sprintf("%sSkywire%s configuration updated\nconfig path: %s%s%s", colorBlue, colorReset, colorPurple, resolved.configPath, colorReset))

		// PKGENV mode: keep the systemd drop-in in sync with
		// SKYWIRE_USER. When SKYWIRE_USER is set, write a drop-in
		// pinning User= and chown the install to that user. When it's
		// unset (or the operator switched away from it), tear down a
		// previously-written drop-in so the unit reverts to running
		// as root — otherwise the stale `User=<old_user>` would
		// either fail CHDIR (if that user no longer has access to
		// the install) or fail the visor's `--pkg requires root`
		// pre-run check (because the drop-in still pins a non-root
		// User= even though the operator's policy changed).
		//
		// Only meaningful when invoked as root: chowning root-owned
		// files and writing under /etc/systemd/system/ both need it.
		// When run as the target user already, we skip both branches
		// and let the operator manage them out of band.
		if !resolved.usrEnv && os.Geteuid() == 0 {
			daemonReload := false
			switch {
			case resolved.skywireUser != "":
				if err := writeSystemdDropIn(resolved.skywireUser); err != nil {
					fmt.Printf("%sWarning:%s could not write systemd drop-in %s: %v\n", colorYellow, colorReset, systemdDropIn, err)
				} else {
					msg3(fmt.Sprintf("Wrote %s (User=%s)", systemdDropIn, resolved.skywireUser))
					daemonReload = true
				}
				if err := chownInstall(resolved.skywireUser); err != nil {
					fmt.Printf("%sWarning:%s chown %s failed: %v\n", colorYellow, colorReset, skyenv.SkywirePath, err)
				}
			default:
				// SKYWIRE_USER unset → ensure no stale drop-in is
				// present that would still pin the unit to a
				// non-root user. Idempotent: silent when there's
				// nothing to remove.
				if _, statErr := os.Stat(systemdDropIn); statErr == nil {
					if err := os.Remove(systemdDropIn); err != nil {
						fmt.Printf("%sWarning:%s could not remove stale drop-in %s: %v\n", colorYellow, colorReset, systemdDropIn, err)
					} else {
						msg3(fmt.Sprintf("Removed stale %s (SKYWIRE_USER unset)", systemdDropIn))
						// Also drop the parent dir if it's now empty.
						_ = os.Remove(filepath.Dir(systemdDropIn)) //nolint:errcheck
						daemonReload = true
					}
				}
			}
			if daemonReload {
				_ = exec.Command("systemctl", "daemon-reload").Run() //nolint:errcheck,gosec
			}
		}

		// SKYBIAN auto-start: legacy appliance bootstrap. Limited to
		// PKGENV mode; user-mode appliances aren't a thing.
		if !resolved.useUserUnit && os.Getenv("SKYBIAN") == "true" {
			msg3("Enabling skywire service and starting...")
			_ = exec.Command("systemctl", "enable", "--now", "skywire.service").Run() //nolint:errcheck,gosec
		}

		restartOrPrompt(resolved)

		// Public key — read from the freshly-generated config rather
		// than spawning another `skywire cli visor pk` (which would
		// race against a still-restarting visor RPC port).
		conf, err := visorconfig.ReadFile(resolved.configPath)
		pubkey := ""
		if err == nil && conf != nil {
			pubkey = conf.PK.Hex()
		}

		if pubkey != "" {
			msg2(fmt.Sprintf("Visor Public Key:\n%s%s%s", colorGreen, pubkey, colorReset))
		}

		isHypervisor := conf != nil && conf.Hypervisor != nil && conf.Hypervisor.Enable

		if isHypervisor {
			msg2(fmt.Sprintf("Hypervisor UI Starting now on:\n%shttp://127.0.0.1:8000%s", colorRed, colorReset))
			if pubkey != "" {
				vpnURL := fmt.Sprintf("http://127.0.0.1:8000/#/vpn/%s", pubkey)
				msg2(fmt.Sprintf("Use the vpn:\n%s%s%s", colorRed, vpnURL, colorReset))
			}

			lanIPs := getLANIPs()
			if len(lanIPs) > 0 {
				hvURLs := "Hypervisor UI LAN access:"
				for _, ip := range lanIPs {
					hvURLs += fmt.Sprintf("\n%shttp://%s:8000%s", colorYellow, ip, colorReset)
				}
				msg2(hvURLs)
			}
		} else {
			msg2(fmt.Sprintf("%sSkywire%s starting without Hypervisor UI", colorBlue, colorReset))
		}

		if conf != nil && len(conf.Hypervisors) > 0 {
			hvPKs := ""
			for _, hvPK := range conf.Hypervisors {
				hvPKs += hvPK.String() + "\n"
			}
			msg2(fmt.Sprintf("Remote Hypervisor Public Key:\n%s%s%s", colorPurple, strings.TrimSpace(hvPKs), colorReset))
		}

		rewardOut, err := exec.Command("skywire", "cli", "reward", "-r").Output() //nolint:gosec
		if err == nil && len(rewardOut) > 0 {
			msg2(fmt.Sprintf("skycoin reward address:\n%s%s%s", colorGreen, strings.TrimSpace(string(rewardOut)), colorReset))
		}

		if autoconfigVerbose {
			printWelcome(pubkey, isHypervisor)
		}
	},
}

// resolveConfig reads the skyenv file and translates it to the
// concrete answers downstream code needs. Falls back gracefully to
// the legacy PKGENV-on-root behavior when the file is silent.
func resolveConfig() resolvedConfig {
	r := resolvedConfig{}
	r.skyenvPath = os.Getenv("SKYENV")
	if r.skyenvPath == "" {
		if _, err := os.Stat("/etc/skywire.conf"); err == nil {
			r.skyenvPath = "/etc/skywire.conf"
		}
	}

	r.pkgEnv = cmdutil.SkyenvBool("${PKGENV:-false}", r.skyenvPath)
	r.usrEnv = cmdutil.SkyenvBool("${USRENV:-false}", r.skyenvPath)
	r.skywireUser = cmdutil.SkyenvString("${SKYWIRE_USER:-}", r.skyenvPath)

	// Implicit fallback when neither flag is set in the env file:
	// root → package mode, otherwise user mode. Mirrors what the
	// `config gen` defaults already produce.
	if !r.pkgEnv && !r.usrEnv {
		if os.Geteuid() == 0 {
			r.pkgEnv = true
		} else {
			r.usrEnv = true
		}
	}

	switch {
	case r.usrEnv:
		r.configPath = filepath.Join(visorconfig.HomePath(), skyenv.ConfigName)
		r.useUserUnit = true
	default: // pkgEnv
		r.configPath = visorconfig.SkywireConfig()
		r.useUserUnit = false
	}
	return r
}

// generateConfig invokes `skywire cli config gen` with the mode
// flag (-p or -u) matching what resolveConfig already decided. We
// intentionally do NOT let the subprocess redo the resolution —
// autoconfig has an implicit "euid==0 → PKGENV" fallback when the
// env file leaves both PKGENV and USRENV commented out, but
// `cli config gen` does not, so without explicit -p/-u the
// subprocess writes the config to gen's own default path (working-
// directory ./skywire-config.json) instead of the system path
// autoconfig is expecting at r.configPath. The mismatch surfaces
// later as the FATAL `expected config file not found` from
// autoconfig's post-Stat check and aborts the postinst with
// exit 100. Propagating the resolved mode explicitly closes the gap.
func generateConfig(r resolvedConfig, hvArg string) error {
	// `-w` / `--hide` suppresses the generated config from being
	// echoed to stdout. autoconfig is meant for unattended
	// systemd/postinst invocation — dumping the visor's SK + every
	// service URL through the terminal at every install is both
	// noisy (the file is the authoritative copy) and a perceived
	// secret-leak risk on shared terminals / CI logs. The Stdout
	// pipe we hook up below is still useful for errors; `-w` only
	// hides the success-path JSON dump.
	args := []string{"cli", "config", "gen", "-r", "-w"}

	switch {
	case r.pkgEnv:
		args = append(args, "-p")
	case r.usrEnv:
		args = append(args, "-u")
	}

	switch hvArg {
	case "0":
		args = append(args, "-i")
	case "1":
		// no hypervisor
	case "":
		// New install? Default to enabling the local hypervisor.
		if _, err := os.Stat(r.configPath); os.IsNotExist(err) {
			args = append(args, "-i")
		}
	default:
		args = append(args, "-j", hvArg)
	}

	envPrefix := ""
	if r.skyenvPath != "" {
		envPrefix = fmt.Sprintf("SKYENV=%s ", r.skyenvPath)
	}
	msg3(fmt.Sprintf("Generating skywire config with command:\n  %s%sskywire %s%s", colorCyan, envPrefix, strings.Join(args, " "), colorReset))

	// Forward stdout and stderr to the parent so any error from
	// `cli config gen` (missing service-config.json, bad SK, write
	// permission denied, etc.) reaches the operator's apt output /
	// journalctl. Previously these were both /dev/null'd, so a
	// failing gen exited 0-or-noise and autoconfig only noticed
	// downstream — by the time the FATAL Stat fired, the actual
	// cause was lost.
	cmd := exec.Command("skywire", args...) //nolint:gosec
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if r.skyenvPath != "" {
		cmd.Env = append(cmd.Env, "SKYENV="+r.skyenvPath)
	}
	return cmd.Run()
}

// writeSystemdDropIn writes a one-section drop-in that pins User=
// (and Group=) on the package-shipped skywire.service. Idempotent —
// rewrite produces identical content when called repeatedly with the
// same user.
func writeSystemdDropIn(username string) error {
	content := fmt.Sprintf("# Managed by `skywire autoconfig`. Edits will be overwritten.\n[Service]\nUser=%s\nGroup=%s\n", username, username)
	// Systemd drop-in dir + file are intentionally world-readable.
	// systemctl reads these as root regardless, but the operator may
	// also want to inspect them without sudo.
	if err := os.MkdirAll(filepath.Dir(systemdDropIn), 0o755); err != nil { //nolint:gosec
		return err
	}
	return os.WriteFile(systemdDropIn, []byte(content), 0o644) //nolint:gosec
}

// chownInstall recursively chowns the package install path to the
// configured user. Skipped silently if the user doesn't exist
// (common during a partial install where postinstall hasn't created
// the user yet); the operator sees a warning instead.
func chownInstall(username string) error {
	u, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("user %q not found: %w", username, err)
	}
	uid, _ := strconv.Atoi(u.Uid) //nolint:errcheck
	gid, _ := strconv.Atoi(u.Gid) //nolint:errcheck
	root := skyenv.SkywirePath
	// Walk root is the package install path (e.g. /opt/skywire) under
	// our control; symlink-TOCTOU isn't a realistic threat for an
	// install-time chown of paths the package itself just laid down.
	return filepath.Walk(root, func(path string, _ os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return os.Lchown(path, uid, gid) //nolint:gosec
	})
}

// restartOrPrompt branches on the resolved mode to pick the right
// systemctl invocation. Active unit → restart; inactive → print the
// command the operator should run to enable+start it. Never tries to
// auto-enable: respects operator intent.
func restartOrPrompt(r resolvedConfig) {
	systemctlArgs := []string{}
	if r.useUserUnit {
		systemctlArgs = append(systemctlArgs, "--user")
	}

	checkArgs := append(append([]string{}, systemctlArgs...), "is-active", "--quiet", "skywire")
	if exec.Command("systemctl", checkArgs...).Run() == nil { //nolint:gosec
		restartArgs := append(append([]string{}, systemctlArgs...), "restart", "skywire")
		msg3("Restarting skywire service…")
		_ = exec.Command("systemctl", restartArgs...).Run() //nolint:errcheck,gosec
		return
	}

	startCmd := "systemctl"
	if r.useUserUnit {
		startCmd += " --user"
	}
	msg2(fmt.Sprintf("Start the skywire service with:\n\t%s%s enable --now skywire%s", colorRed, startCmd, colorReset))
}

func printWelcome(pubkey string, isHypervisor bool) {
	rewardOut, err := exec.Command("skywire", "cli", "reward", "-r").Output()
	if err == nil && len(rewardOut) > 0 {
		msg2(fmt.Sprintf("skycoin reward address:\n%s%s%s", colorGreen, strings.TrimSpace(string(rewardOut)), colorReset))
		msg2(fmt.Sprintf("reward metrics:\n%shttps://theskywirenetwork.net%s", colorBlue, colorReset))
		msg2(fmt.Sprintf("distribution notifications:\n%shttps://t.me/skywire_reward%s", colorBlue, colorReset))
	} else {
		msg2(fmt.Sprintf("reward eligibility rules:\n%shttps://github.com/skycoin/skywire/blob/develop/mainnet_rules.md%s", colorYellow, colorReset))
		msg2(fmt.Sprintf("set your skycoin reward address:\n%sskywire cli %sreward %s<skycoin-address>%s", colorCyan, colorYellow, colorGreen, colorReset))
	}

	msg2(fmt.Sprintf("support:\n%shttps://t.me/skywire%s", colorBlue, colorReset))

	if isHypervisor && pubkey != "" {
		msg2("run the following command on OTHER NODES to set this one as the hypervisor:")
		fmt.Printf("%sskywire autoconfig %s%s%s\n", colorCyan, colorYellow, pubkey, colorReset)
		msg2(fmt.Sprintf("to see this text again run: %sskywire autoconfig%s", colorCyan, colorReset))
	}
}

func getLANIPs() []string {
	var ips []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				ips = append(ips, ipnet.IP.String())
			}
		}
	}
	return ips
}

func msg2(msg string) {
	fmt.Printf("%s ->%s%s %s%s\n", colorCyan, colorReset, colorBold, msg, colorReset)
}

func msg3(msg string) {
	fmt.Printf("%s -->%s%s %s%s\n", colorBlue, colorReset, colorBold, msg, colorReset)
}
