// Package cliresolver cmd/skywire-cli/commands/resolver/ca.go
//
// `skywire cli resolver ca {gen|install|uninstall|path|fingerprint}`
// — local certificate authority lifecycle for the resolver's
// optional TLS MITM mode.
//
// The CA is a one-per-visitor on-disk keypair, name-constrained to
// .skynet and .dmsg, used to sign per-host leaf certs the resolver
// presents to the browser. See pkg/skynetca for the trust model and
// docs/skynet-tls.md for the install procedure.
//
// `gen`, `path`, and `fingerprint` are pure filesystem operations
// the visitor can run without root. `install` and `uninstall` touch
// the system trust store and typically require sudo.
package cliresolver

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/skynetca"
)

var (
	caForce        bool
	caInstallPrint bool
)

func init() {
	caGenCmd.Flags().BoolVar(&caForce, "force", false, "overwrite an existing CA")
	caInstallCmd.Flags().BoolVar(&caInstallPrint, "print", false, "print the install commands instead of running them")

	caCmd.AddCommand(caGenCmd, caInstallCmd, caUninstallCmd, caPathCmd, caFingerprintCmd)
	RootCmd.AddCommand(caCmd)
}

var caCmd = &cobra.Command{
	Use:   "ca",
	Short: "Manage the local resolver CA (TLS MITM mode)",
	Long: `Manage the local certificate authority used by the resolver's
optional TLS MITM mode. The CA is generated on this machine and
name-constrained to *.skynet and *.dmsg via the X.509
nameConstraints extension; even though it is installed in the OS
trust store it cannot issue valid certs for any other domain.

Typical first-time setup (visitor-side):

  skywire cli resolver ca gen        # create the CA
  skywire cli resolver ca install    # add to OS trust store (sudo)

Then enable TLS MITM in the visor config (skywire-config.json) by
setting "tls_mitm": true and the ca paths under skynet_web and/or
dmsg_web, and restart the visor.
`,
}

var caGenCmd = &cobra.Command{
	Use:   "gen",
	Short: "Generate a new local CA",
	Run: func(cmd *cobra.Command, _ []string) {
		certPath, keyPath, err := defaultCAPaths()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if !caForce {
			if _, err := os.Stat(certPath); err == nil {
				internal.PrintFatalError(cmd.Flags(),
					fmt.Errorf("CA already exists at %s; use --force to overwrite", certPath))
			}
		}
		ca, key, err := skynetca.GenerateCA(skynetca.CAOptions{})
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("generate CA: %w", err))
		}
		if err := skynetca.SaveCA(ca, key, certPath, keyPath); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("save CA: %w", err))
		}
		fp := skynetca.Fingerprint(ca)
		fmt.Printf("CA generated.\n  cert:        %s\n  key:         %s\n  fingerprint: %s\n",
			certPath, keyPath, fp)
		fmt.Println()
		fmt.Println("Next: skywire cli resolver ca install")
	},
}

var caInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Add the CA to the OS trust store",
	Long: `Copies the local CA into the system trust store. On Linux
this means /etc/ca-certificates/trust-source/anchors (Arch),
/usr/local/share/ca-certificates (Debian/Ubuntu), or
/etc/pki/ca-trust/source/anchors (Fedora/RHEL), followed by the
distro's update command.

Requires root for the system store. Use --print to see the
commands without running them.

NOTE: Firefox keeps its own NSS store and is not updated by the
system command. See docs/skynet-tls.md for the per-profile
certutil incantation.

NOTE: macOS and Windows are not yet supported by this subcommand.
`,
	Run: func(cmd *cobra.Command, _ []string) {
		certPath, _, err := defaultCAPaths()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if _, err := os.Stat(certPath); err != nil {
			internal.PrintFatalError(cmd.Flags(),
				fmt.Errorf("CA not found at %s; run `skywire cli resolver ca gen` first", certPath))
		}
		if runtime.GOOS != "linux" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf(
				"automatic install is Linux-only in this release; see docs/skynet-tls.md for %s", runtime.GOOS))
		}
		anchorsDir, updateCmd, err := detectLinuxTrustStore()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		dst := filepath.Join(anchorsDir, "skywire-resolver.crt")

		copy := []string{"sudo", "install", "-m", "0644", certPath, dst}
		update := append([]string{"sudo"}, updateCmd...)

		if caInstallPrint {
			fmt.Println("# Run these commands to install the CA into the system trust store:")
			fmt.Println(strings.Join(copy, " "))
			fmt.Println(strings.Join(update, " "))
			fmt.Println()
			fmt.Println("# Firefox uses its own NSS store; see docs/skynet-tls.md.")
			return
		}

		if err := runVisible(copy); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("install CA: %w", err))
		}
		if err := runVisible(update); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("trust update: %w", err))
		}
		fmt.Printf("CA installed at %s\n", dst)
		fmt.Println("Firefox uses its own NSS store; see docs/skynet-tls.md.")
	},
}

var caUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the CA from the OS trust store",
	Run: func(cmd *cobra.Command, _ []string) {
		if runtime.GOOS != "linux" {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf(
				"automatic uninstall is Linux-only in this release; see docs/skynet-tls.md for %s", runtime.GOOS))
		}
		anchorsDir, updateCmd, err := detectLinuxTrustStore()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		dst := filepath.Join(anchorsDir, "skywire-resolver.crt")
		if err := runVisible([]string{"sudo", "rm", "-f", dst}); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("rm CA: %w", err))
		}
		if err := runVisible(append([]string{"sudo"}, updateCmd...)); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("trust update: %w", err))
		}
		fmt.Printf("Removed %s\n", dst)
	},
}

var caPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print where the local CA cert and key are stored",
	Run: func(cmd *cobra.Command, _ []string) {
		certPath, keyPath, err := defaultCAPaths()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Printf("cert: %s\nkey:  %s\n", certPath, keyPath)
	},
}

var caFingerprintCmd = &cobra.Command{
	Use:   "fingerprint",
	Short: "Print the SHA-256 fingerprint of the local CA",
	Run: func(cmd *cobra.Command, _ []string) {
		certPath, keyPath, err := defaultCAPaths()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		ca, _, err := skynetca.LoadCA(certPath, keyPath)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("load CA: %w", err))
		}
		fmt.Println(skynetca.Fingerprint(ca))
	},
}

// defaultCAPaths returns the conventional on-disk location for the
// resolver CA: ~/.skywire/resolver/ca.{crt,key}.
func defaultCAPaths() (certPath, keyPath string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("locate home dir: %w", err)
	}
	dir := filepath.Join(home, ".skywire", "resolver")
	return filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key"), nil
}

// detectLinuxTrustStore inspects /etc/os-release to locate the
// distro's anchors directory and the system trust-update command.
func detectLinuxTrustStore() (anchorsDir string, updateCmd []string, err error) {
	osRelease, _ := os.ReadFile("/etc/os-release") //nolint:errcheck,gosec
	id := osReleaseField(string(osRelease), "ID")
	idLike := osReleaseField(string(osRelease), "ID_LIKE")
	combined := id + " " + idLike
	switch {
	case strings.Contains(combined, "arch"):
		return "/etc/ca-certificates/trust-source/anchors", []string{"trust", "extract-compat"}, nil
	case strings.Contains(combined, "debian") || strings.Contains(combined, "ubuntu"):
		return "/usr/local/share/ca-certificates", []string{"update-ca-certificates"}, nil
	case strings.Contains(combined, "fedora") || strings.Contains(combined, "rhel") || strings.Contains(combined, "centos"):
		return "/etc/pki/ca-trust/source/anchors", []string{"update-ca-trust"}, nil
	}
	return "", nil, errors.New("unrecognized Linux distro; install manually per docs/skynet-tls.md")
}

func osReleaseField(content, key string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key+"=") {
			return strings.Trim(strings.TrimPrefix(line, key+"="), `"`)
		}
	}
	return ""
}

func runVisible(argv []string) error {
	if len(argv) == 0 {
		return errors.New("empty command")
	}
	fmt.Printf("$ %s\n", strings.Join(argv, " "))
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}
