// Package commands cmd/skywire/commands/autoconfig.go
package commands

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/deployment"
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

var autoconfigVerbose bool

func init() {
	autoconfigCmd.Flags().BoolVarP(&autoconfigVerbose, "verbose", "v", false, "show reward address, support links, and other details")
	RootCmd.AddCommand(autoconfigCmd)
}

var autoconfigCmd = &cobra.Command{
	Use:    "autoconfig",
	Short:  "Automatic visor configuration for packages",
	Long:   `Automatic visor configuration for package installations. Generates config, manages systemd service, and displays helpful information.`,
	Hidden: true,
	Run: func(_ *cobra.Command, args []string) {
		// Check for root
		if os.Geteuid() != 0 {
			fmt.Println("root permissions required")
			os.Exit(1)
		}

		// Check NOAUTOCONFIG
		if os.Getenv("NOAUTOCONFIG") == "true" {
			fmt.Println("autoconfiguration disabled. to configure and start skywire run: skywire autoconfig")
			os.Exit(0)
		}

		// Create local directory if needed
		if err := os.MkdirAll("/opt/skywire/local/custom", 0750); err != nil {
			fmt.Printf("Warning: could not create local directory: %v\n", err)
		}

		msg2("Configuring skywire")

		// Print version
		versionOut, err := exec.Command("skywire", "-v").Output()
		if err == nil {
			version := strings.TrimSpace(string(versionOut))
			if !strings.Contains(version, "unknown") {
				msg2(fmt.Sprintf("version: %s", strings.Fields(version)[len(strings.Fields(version))-1]))
			}
		}

		// Handle hypervisor argument (remote PK or 0/1)
		hvArg := ""
		if len(args) > 0 {
			hvArg = args[0]
		}

		// Generate config
		if err := generateConfig(hvArg); err != nil {
			fmt.Printf("%s>>> FATAL:%s %v\n", colorRed, colorReset, err)
			os.Exit(1)
		}

		// Verify config was created
		if _, err := os.Stat("/opt/skywire/skywire.json"); os.IsNotExist(err) {
			fmt.Printf("%s>>> FATAL:%s expected config file not found at /opt/skywire/skywire.json\n", colorRed, colorReset)
			os.Exit(100)
		}
		msg3(fmt.Sprintf("%sSkywire%s configuration updated\nconfig path: %s/opt/skywire/skywire.json%s", colorBlue, colorReset, colorPurple, colorReset))

		// Handle SKYBIAN auto-start
		isSkybian := os.Getenv("SKYBIAN") == "true"
		if isSkybian {
			msg3("Enabling skywire service and starting...")
			//nolint:errcheck,gosec
			exec.Command("systemctl", "enable", "--now", "skywire.service").Run()
		}

		// Restart service if already running
		checkActive := exec.Command("systemctl", "is-active", "--quiet", "skywire")
		if checkActive.Run() == nil {
			msg3("Restarting skywire.service...")
			//nolint:errcheck,gosec
			exec.Command("systemctl", "restart", "skywire").Run()
		} else {
			msg2(fmt.Sprintf("Start the skywire service with:\n\t%ssystemctl start skywire%s", colorRed, colorReset))
		}

		// Restart test service only if already running
		checkTestActive := exec.Command("systemctl", "is-active", "--quiet", "skywire-test")
		if checkTestActive.Run() == nil {
			msg3("Restarting skywire-test.service...")
			exec.Command("systemctl", "restart", "skywire-test").Run() //nolint:errcheck,gosec
		}

		// Get public key
		pkOut, err := exec.Command("skywire", "cli", "visor", "pk", "-p").Output()
		pubkey := ""
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(pkOut)), "\n")
			if len(lines) > 0 {
				pubkey = lines[len(lines)-1]
			}
		}

		if pubkey != "" {
			msg2(fmt.Sprintf("Visor Public Key:\n%s%s%s", colorGreen, pubkey, colorReset))
		}

		// Load config to check hypervisor status
		conf, err := visorconfig.ReadFile("/opt/skywire/skywire.json")
		isHypervisor := err == nil && conf != nil && conf.Hypervisor != nil && conf.Hypervisor.Enable

		// Show hypervisor URLs if applicable
		if isHypervisor {
			msg2(fmt.Sprintf("Hypervisor UI Starting now on:\n%shttp://127.0.0.1:8000%s", colorRed, colorReset))
			if pubkey != "" {
				vpnURL := fmt.Sprintf("http://127.0.0.1:8000/#/vpn/%s", pubkey)
				msg2(fmt.Sprintf("Use the vpn:\n%s%s%s", colorRed, vpnURL, colorReset))
			}

			// Show LAN IPs
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

		// Show hypervisor PKs if configured
		if conf != nil && len(conf.Hypervisors) > 0 {
			hvPKs := ""
			for _, hvPK := range conf.Hypervisors {
				hvPKs += hvPK.String() + "\n"
			}
			msg2(fmt.Sprintf("Remote Hypervisor Public Key:\n%s%s%s", colorPurple, strings.TrimSpace(hvPKs), colorReset))
		}

		// Always show reward address if set
		rewardOut, err := exec.Command("skywire", "cli", "reward", "-r").Output() //nolint:gosec
		if err == nil && len(rewardOut) > 0 {
			msg2(fmt.Sprintf("skycoin reward address:\n%s%s%s", colorGreen, strings.TrimSpace(string(rewardOut)), colorReset))
		}

		// Welcome message (only with --verbose)
		if autoconfigVerbose {
			printWelcome(pubkey, isHypervisor)
		}
	},
}

func generateConfig(hvArg string) error {
	// Build config gen command.
	// -r: regen existing config, -p: package mode (forces /opt/skywire paths).
	// All other flags come from SKYENV (/etc/skywire.conf).
	args := []string{"cli", "config", "gen", "-r", "-p"}

	// Determine SKYENV path
	skyenv := os.Getenv("SKYENV")
	if skyenv == "" {
		if _, err := os.Stat("/etc/skywire.conf"); err == nil {
			skyenv = "/etc/skywire.conf"
		}
	}

	// Handle hypervisor argument (only if not already in SKYENV)
	switch hvArg {
	case "0":
		args = append(args, "-i")
	case "1":
		// No hypervisor
	case "":
		// Create hypervisor by default for new installs
		if _, err := os.Stat("/opt/skywire/skywire.json"); os.IsNotExist(err) {
			args = append(args, "-i")
		}
	default:
		args = append(args, "-j", hvArg)
	}

	envPrefix := ""
	if skyenv != "" {
		envPrefix = fmt.Sprintf("SKYENV=%s ", skyenv)
	}
	msg3(fmt.Sprintf("Generating skywire config with command:\n  %s%sskywire %s%s", colorCyan, envPrefix, strings.Join(args, " "), colorReset))

	cmd := exec.Command("skywire", args...) //nolint:gosec
	cmd.Stdout = nil
	cmd.Stderr = nil // suppress DMSG debug logging
	cmd.Env = os.Environ()
	if skyenv != "" {
		cmd.Env = append(cmd.Env, "SKYENV="+skyenv)
	}
	if err := cmd.Run(); err != nil {
		return err
	}

	// Generate test deployment config with the same keys.
	// Skip if:
	// - Custom service config URL is set (no test counterpart to fetch from)
	// - Test deployment is not defined (SKYDEPLOY with only prod section)
	if os.Getenv("SVCCONFADDR") == "" && deployment.Test.DmsgDiscovery != "" {
		if err := generateTestConfig(hvArg); err != nil {
			// Non-fatal: test config is optional
			fmt.Printf("%sWarning:%s test config generation failed: %v\n", colorYellow, colorReset, err)
		}
	}
	return nil
}

func generateTestConfig(hvArg string) error {
	testConf := "/opt/skywire/skywire-test.json"

	args := []string{"cli", "config", "gen",
		"-r",           // regen (reuses SK if test config exists, or from SKYENV)
		"-p",           // package mode
		"-t",           // test deployment
		"-o", testConf, // separate config file (overrides -p default path)
	}

	// Mirror hypervisor setting from prod
	conf, _ := visorconfig.ReadFile("/opt/skywire/skywire.json") //nolint:errcheck
	switch hvArg {
	case "0":
		args = append(args, "-i")
	case "":
		if conf != nil && conf.Hypervisor != nil && conf.Hypervisor.Enable {
			args = append(args, "-i")
		}
	default:
		if hvArg != "1" {
			args = append(args, "-j", hvArg)
		}
	}

	// Determine SKYENV path (same as prod config)
	skyenv := os.Getenv("SKYENV")
	if skyenv == "" {
		if _, statErr := os.Stat("/etc/skywire.conf"); statErr == nil {
			skyenv = "/etc/skywire.conf"
		}
	}

	envPrefix := ""
	if skyenv != "" {
		envPrefix = fmt.Sprintf("SKYENV=%s ", skyenv)
	}
	msg3(fmt.Sprintf("Generating test config:\n  %s%sskywire %s%s", colorCyan, envPrefix, strings.Join(args, " "), colorReset))

	cmd := exec.Command("skywire", args...) //nolint:gosec
	cmd.Stdout = nil
	cmd.Stderr = nil // suppress DMSG debug logging
	cmd.Env = os.Environ()
	if skyenv != "" {
		cmd.Env = append(cmd.Env, "SKYENV="+skyenv)
	}
	return cmd.Run()
}

func printWelcome(pubkey string, isHypervisor bool) {
	// Reward address
	rewardOut, err := exec.Command("skywire", "cli", "reward", "-r").Output()
	if err == nil && len(rewardOut) > 0 {
		msg2(fmt.Sprintf("skycoin reward address:\n%s%s%s", colorGreen, strings.TrimSpace(string(rewardOut)), colorReset))
		msg2(fmt.Sprintf("reward metrics:\n%shttps://theskywirenetwork.net%s", colorBlue, colorReset))
		msg2(fmt.Sprintf("distribution notifications:\n%shttps://t.me/skywire_reward%s", colorBlue, colorReset))
	} else {
		msg2(fmt.Sprintf("reward eligibility rules:\n%shttps://github.com/skycoin/skywire/blob/develop/mainnet_rules.md%s", colorYellow, colorReset))
		msg2(fmt.Sprintf("set your skycoin reward address:\n%sskywire cli %sreward %s<skycoin-address>%s", colorCyan, colorYellow, colorGreen, colorReset))
	}

	// Support
	msg2(fmt.Sprintf("support:\n%shttps://t.me/skywire%s", colorBlue, colorReset))

	// Hypervisor instructions
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
