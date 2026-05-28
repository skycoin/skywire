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
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
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

// autoconfigVals holds the runtime values of the autoconfig flags.
// Lives at package level so the Run function can read them; the
// pkg/skywireconfig/autoconfigcmd factory wires the bindings.
var autoconfigVals autoconfigcmd.Values

func init() {
	autoconfigCmd = autoconfigcmd.New(&autoconfigVals)
	// Preserved from the pre-factory inlined version. The factory
	// produces a generic, non-hidden command; the binary makes it
	// hidden so it doesn't pollute the top-level help listing
	// (operators reach it via the autoconfig package install
	// step, not via interactive discovery).
	autoconfigCmd.Hidden = true
	autoconfigCmd.Run = autoconfigRun
	RootCmd.AddCommand(autoconfigCmd)
}

// collectSkyenvEdits walks the autoSet* flags and assembles the list
// of edits to apply to /etc/skywire.conf. Only flags the operator
// actually passed are included — un-passed flags leave the
// corresponding lines untouched.
//
// The flag → SKYENV mapping is authoritatively defined in
// pkg/skywireconfig/autoconfigcmd.EnvMap(); for the actual
// VALUES we read from autoconfigVals via tiny switch helpers.
// Keeping the mapping there means the WASM consumer (apt-repo
// install page) and this Run-path agree on which env var each
// flag writes.
func collectSkyenvEdits(cmd *cobra.Command) []skyenvEdit {
	var edits []skyenvEdit

	// Bool-pair helper: --flag wins over --no-flag if both somehow set,
	// but cobra normally only sees one per invocation.
	addBool := func(key, onName, offName string, on, off bool) {
		switch {
		case cmd.Flags().Changed(onName) && on:
			edits = append(edits, skyenvEdit{Key: key, Value: formatSkyenvBool(true)})
		case cmd.Flags().Changed(offName) && off:
			edits = append(edits, skyenvEdit{Key: key, Value: formatSkyenvBool(false)})
		}
	}
	addSoloBool := func(key, name string, val bool) {
		if cmd.Flags().Changed(name) {
			edits = append(edits, skyenvEdit{Key: key, Value: formatSkyenvBool(val)})
		}
	}
	addString := func(key, name, val string) {
		if cmd.Flags().Changed(name) {
			edits = append(edits, skyenvEdit{Key: key, Value: formatSkyenvString(val)})
		}
	}
	addArray := func(key, name, val string) {
		if cmd.Flags().Changed(name) {
			edits = append(edits, skyenvEdit{Key: key, Value: formatSkyenvBashArray(val)})
		}
	}
	addInt := func(key, name string, val int) {
		if cmd.Flags().Changed(name) {
			edits = append(edits, skyenvEdit{Key: key, Value: formatSkyenvInt(val)})
		}
	}

	// Hypervisor / identity
	addArray("HYPERVISORPKS", "hvpks", autoconfigVals.Hvpks)
	addBool("ISHYPERVISOR", "ishv", "no-ishv", autoconfigVals.Ishv, autoconfigVals.NoIshv)
	addBool("ENABLEPKENDPOINT", "pk-endpoint", "no-pk-endpoint", autoconfigVals.PkEndpoint, autoconfigVals.NoPkEndpoint)
	addString("HVHTTPADDR", "hvaddr", autoconfigVals.HvAddr)
	addString("SK", "sk", autoconfigVals.SecretKey)
	addString("VERSION", "version", autoconfigVals.Version)

	// Visor public/private + autoconnect
	addString("REWARDSKYADDR", "rewardaddr", autoconfigVals.RewardAddr)
	addBool("VISORISPUBLIC", "public", "no-public", autoconfigVals.Public, autoconfigVals.NoPublic)
	addSoloBool("DISPLAYNODEIP", "publicip", autoconfigVals.PublicIP)
	addSoloBool("DISABLEPUBLICAUTOCONN", "disable-public-autoconn", autoconfigVals.DisablePubAuto)

	// Service discovery / deployment
	addSoloBool("TESTENV", "testenv", autoconfigVals.TestEnv)
	addSoloBool("DMSGHTTP", "dmsghttp", autoconfigVals.DmsgHTTP)
	addString("DMSGCONF", "dmsgconf", autoconfigVals.DmsgConf)
	addArray("SVCCONFADDR", "url", autoconfigVals.URL)
	addString("SVCCONF", "svcconf", autoconfigVals.SvcConf)
	addInt("MINDMSGSESS", "minsess", autoconfigVals.MinSess)
	addArray("STUNSERVERS", "stun", autoconfigVals.StunServers)

	// Transport ports
	addInt("STCPRPORT", "stcpr", autoconfigVals.StcprPort)
	addInt("SUDPHPORT", "sudph", autoconfigVals.SudphPort)
	addInt("LANDMSGPORT", "lan-dmsg-port", autoconfigVals.LanDmsgPort)
	addString("LANDMSGPUBLIC", "lan-dmsg-public", autoconfigVals.LanDmsgPublic)

	// Whitelists
	addArray("DMSGPTYPKS", "dmsgpty-pks", autoconfigVals.DmsgptyPks)
	addArray("SURVEYPKS", "survey", autoconfigVals.SurveyPks)
	addArray("ROUTESETUPPKS", "routesetup", autoconfigVals.RouteSetupPKs)
	addArray("TPSETUPPKS", "tpsetup", autoconfigVals.TransportSetup)

	// Route calculation
	addSoloBool("CALCULATEROUTES", "calculate-routes", autoconfigVals.CalculateRoutes)

	// VPN server
	addBool("VPNSERVER", "vpnserver", "no-vpnserver", autoconfigVals.VpnServer, autoconfigVals.NoVpnServer)
	addString("VPNKS", "killsw", autoconfigVals.VpnKillSw)
	addString("ADDVPNPK", "addvpn", autoconfigVals.AddVpn)
	addArray("VPNSERVERWL", "vpnwl", autoconfigVals.VpnWl)
	addString("VPNSEVERSECURE", "secure", autoconfigVals.VpnSecure)
	addString("VPNSEVERNETIFC", "netifc", autoconfigVals.VpnNetIfc)

	// Proxy
	addBool("PROXYSERVER", "proxyserver", "no-proxyserver", autoconfigVals.ProxyServer, autoconfigVals.NoProxyServer)
	addString("PROXYCLIENTPK", "proxyclientpk", autoconfigVals.ProxyClientPK)
	addSoloBool("STARTPROXYCLIENT", "startproxyclient", autoconfigVals.StartProxyCli)
	addArray("PROXYSERVERWL", "proxywl", autoconfigVals.ProxyWl)

	// SOCKS5 web bridges
	addBool("DMSGWEB", "dmsgweb", "no-dmsgweb", autoconfigVals.Dmsgweb, autoconfigVals.NoDmsgweb)
	addBool("SKYNETWEB", "skynetweb", "no-skynetweb", autoconfigVals.Skynetweb, autoconfigVals.NoSkynetweb)
	addString("DMSGWEBUPSTREAM", "dmsgweb-upstream", autoconfigVals.DmsgwebUpstream)
	addString("SKYNETWEBUPSTREAM", "skynetweb-upstream", autoconfigVals.SkynetwebUpstream)

	// Skychat
	addBool("SKYCHAT", "skychat", "no-skychat", autoconfigVals.Skychat, autoconfigVals.NoSkychat)
	addString("SKYCHATADDR", "chataddr", autoconfigVals.ChatAddr)
	addBool("SKYCHATPAIR", "servechatpair", "no-servechatpair", autoconfigVals.ServeChatPair, autoconfigVals.NoServeChatPair)

	// Skymail bridge
	addBool("SKYMAILBRIDGE", "skymail-bridge", "no-skymail-bridge", autoconfigVals.SkymailBridge, autoconfigVals.NoSkymailBridge)

	// Skycoin daemon
	addBool("SKYCOIND", "skycoind", "no-skycoind", autoconfigVals.Skycoind, autoconfigVals.NoSkycoind)
	addString("SKYCOIND_FIBER_TOML", "skycoindfiber", autoconfigVals.SkycoindFiber)
	addString("SKYCOIND_API_SETS", "skycoindapi", autoconfigVals.SkycoindAPI)
	addString("SKYCOIND_USER", "skycoindUSER", autoconfigVals.SkycoindUser)
	addArray("SKYCOIND_INSTANCES", "skycoindinstances", autoconfigVals.SkycoindInstances)
	addString("SKYCOIND_FLAGS", "skycoindflags", autoconfigVals.SkycoindFlags)

	// Skycoin web wallet
	addBool("SKYCOINWEB", "skycoinweb", "no-skycoinweb", autoconfigVals.Skycoinweb, autoconfigVals.NoSkycoinweb)
	addString("SKYCOINWEBADDR", "skycoinwebaddr", autoconfigVals.SkycoinwebAddr)
	addArray("SKYCOINWEBNODES", "skycoinwebnodes", autoconfigVals.SkycoinwebNodes)
	addString("SKYCOINWEBWALLET", "skycoinwebwallet", autoconfigVals.SkycoinwebWallet)
	addString("SKYCOINWEBUSER", "skycoinwebuser", autoconfigVals.SkycoinwebUser)

	// Visor runtime
	addString("BINPATH", "binpath", autoconfigVals.BinPath)
	addString("LOGLVL", "loglvl", autoconfigVals.LogLevel)
	addString("SHUTDOWNTIMEOUT", "timeout", autoconfigVals.ShutdownTimeout)
	addString("REGTIMEOUT", "regtimeout", autoconfigVals.RegTimeout)

	return edits
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

// autoconfigCmd is constructed in init() via the autoconfigcmd factory
// so the flag set is shared with WASM consumers (apt-repo install
// page). Use, Short, Long, and flag bindings all come from the
// factory; this file only contributes Hidden=true and the Run body.
var autoconfigCmd *cobra.Command

// autoconfigRun is the Run function attached to autoconfigCmd in
// init(). Extracted from the pre-factory inline closure so the
// init body stays small.
func autoconfigRun(cmd *cobra.Command, args []string) {
	if os.Getenv("NOAUTOCONFIG") == "true" {
		fmt.Println("autoconfiguration disabled. to configure and start skywire run: skywire autoconfig")
		os.Exit(0)
	}

	msg2("Configuring skywire")

	// Apply any operator-requested skywire.conf edits before
	// the rest of autoconfig sources the file. Each `--flag`
	// rewrites the corresponding env line in place (uncomment +
	// new value), preserving every other line. The subsequent
	// `cli config gen` invocation reads the updated file via
	// SKYENV so the new values take effect on the same run.
	//
	// Resolves the skyenv path FIRST so we know which file to
	// edit — same lookup the downstream stages use, just lifted
	// up to do the edits first.
	if edits := collectSkyenvEdits(cmd); len(edits) > 0 {
		pre := resolveConfig()
		target := pre.skyenvPath
		if target == "" {
			target = defaultSkyenvPath()
		}
		if err := updateSkyenvFile(target, edits); err != nil {
			fmt.Printf("%s>>> FATAL:%s could not update %s: %v\n", colorRed, colorReset, target, err)
			os.Exit(1)
		}
		editedKeys := make([]string, 0, len(edits))
		for _, e := range edits {
			editedKeys = append(editedKeys, e.Key)
		}
		msg3(fmt.Sprintf("Updated %s%s%s: %s", colorPurple, target, colorReset, strings.Join(editedKeys, " ")))
	}

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
			return "implicit fallback (no " + defaultSkyenvPath() + ")"
		}())
		fmt.Printf("            if %s is commented out in %s, uncomment it and re-run `skywire autoconfig`\n",
			mode, func() string {
				if resolved.skyenvPath != "" {
					return resolved.skyenvPath
				}
				return defaultSkyenvPath()
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
	if runtime.GOOS == "linux" && !resolved.usrEnv && os.Geteuid() == 0 {
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
	// Linux-only — Windows service registration is the Phase 2
	// piece of the autoconfig parity project (see project memory
	// project_windows_autoconfig_parity.md). For now Windows
	// operators run `skywire visor` directly after autoconfig.
	if runtime.GOOS == "linux" && !resolved.useUserUnit && os.Getenv("SKYBIAN") == "true" {
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

	if autoconfigVals.Verbose {
		printWelcome(pubkey, isHypervisor)
	}
}

// defaultSkyenvPath returns the canonical SKYENV file location for
// the host OS. Linux uses /etc/skywire.conf (FHS); Windows uses
// %ProgramData%\Skywire\skywire.conf (the equivalent system-wide
// configuration path — picked up by an elevated Notepad or the
// %ProgramData% environment variable). Anything else falls back to
// the Linux path so darwin / freebsd builds keep their previous
// behavior; they're not formally supported here but shouldn't regress.
func defaultSkyenvPath() string {
	if runtime.GOOS == "windows" {
		if pd := os.Getenv("ProgramData"); pd != "" {
			return filepath.Join(pd, "Skywire", "skywire.conf")
		}
		return `C:\ProgramData\Skywire\skywire.conf`
	}
	return "/etc/skywire.conf"
}

// resolveConfig reads the skyenv file and translates it to the
// concrete answers downstream code needs. Falls back gracefully to
// the legacy PKGENV-on-root behavior when the file is silent.
func resolveConfig() resolvedConfig {
	r := resolvedConfig{}
	r.skyenvPath = os.Getenv("SKYENV")
	if r.skyenvPath == "" {
		candidate := defaultSkyenvPath()
		if _, err := os.Stat(candidate); err == nil {
			r.skyenvPath = candidate
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

// generateConfig invokes `skywire cli config gen` with regen +
// hide flags. -p/-u are NOT passed: cli config gen reads PKGENV /
// USRENV from the SKYENV file via scriptExecBool, so the env-var
// path picks up the same resolution autoconfig made — without
// hard-coding the mode in the args (the operator's intent lives in
// /etc/skywire.conf, not in this Cmd line).
func generateConfig(r resolvedConfig, hvArg string) error {
	// printableArgs is what we PRINT for the operator to copy-paste.
	// Intentionally omits -w (we suppress the noisy fetch logs
	// ourselves) and -p/-u (resolved via SKYENV vars). If the
	// install fails, the operator can paste this exact line and see
	// the full debug logging that we hid.
	printableArgs := []string{"cli", "config", "gen", "-r"}

	// args is what we ACTUALLY pass. -w suppresses the success-path
	// JSON dump (echoing the SK + every service URL on every install
	// is noisy and a perceived secret-leak risk on shared terminals).
	args := []string{"cli", "config", "gen", "-r", "-w"}

	switch hvArg {
	case "0":
		args = append(args, "-i")
		printableArgs = append(printableArgs, "-i")
	case "1":
		// no hypervisor
	case "":
		// New install? Default to enabling the local hypervisor.
		if _, err := os.Stat(r.configPath); os.IsNotExist(err) {
			args = append(args, "-i")
			printableArgs = append(printableArgs, "-i")
		}
	default:
		args = append(args, "-j", hvArg)
		printableArgs = append(printableArgs, "-j", hvArg)
	}

	envPrefix := ""
	if r.skyenvPath != "" {
		envPrefix = fmt.Sprintf("SKYENV=%s ", r.skyenvPath)
	}
	msg3(fmt.Sprintf("Generating skywire config with command:\n  %s%sskywire %s%s", colorCyan, envPrefix, strings.Join(printableArgs, " "), colorReset))

	// Suppress the subprocess's stdout/stderr by default. The DMSG
	// fetch chatter ([INFO]/[DEBUG] lines) and -w's hidden JSON dump
	// both go through here and are noise on the happy path. Buffer
	// stderr; on failure flush it so the operator can see what
	// broke (missing service-config.json, bad SK, write permission
	// denied, etc.). If the operator wants the verbose path, the
	// printableArgs above show the same command without -w to paste
	// into a terminal.
	var stderrBuf bytes.Buffer
	cmd := exec.Command("skywire", args...) //nolint:gosec
	cmd.Stdout = os.Stdout
	cmd.Stderr = &stderrBuf
	cmd.Env = os.Environ()
	if r.skyenvPath != "" {
		cmd.Env = append(cmd.Env, "SKYENV="+r.skyenvPath)
	}
	if err := cmd.Run(); err != nil {
		if stderrBuf.Len() > 0 {
			fmt.Fprint(os.Stderr, stderrBuf.String())
		}
		return err
	}
	return nil
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
//
// Windows: POSIX uid/gid don't translate. SKYWIRE_USER on Windows is
// resolved at Service install time by setting the service's run-as
// account, not by chmodding files — see project memory
// project_windows_autoconfig_parity.md Phase 2 for the ServiceInstall
// integration. Returns nil here so the caller's "Warning: chown
// failed" branch doesn't fire for a no-op.
func chownInstall(username string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
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
// service-manager invocation. Linux: systemctl. Windows: defer to
// Phase 2 — for now we just tell the operator the manual command
// they need (`skywire visor -c <path>`) until the Windows Service
// registration ships.
//
// Active unit → restart; inactive → print the start instructions.
// Never tries to auto-enable: respects operator intent.
func restartOrPrompt(r resolvedConfig) {
	if runtime.GOOS == "windows" {
		msg2(fmt.Sprintf("Start skywire on Windows with (admin PowerShell):\n\t%sskywire visor -c %q%s",
			colorRed, r.configPath, colorReset))
		return
	}

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
