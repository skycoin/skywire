// Package clirelease cmd/skywire-cli/commands/release/root.go c4-vis-cli
//
// `skywire cli release` creates release signing keys, signs checksum files
// and verifies them against the keys built into this binary.
package clirelease

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/releasesig"
)

var (
	keygenName   string
	keygenOut    string
	signKeyFile  string
	signOut      string
	verifySig    string
	verifyPubKey []string
)

// RootCmd is the release command group.
var RootCmd = rootCmdBuild()

func rootCmdBuild() *cobra.Command {
	root := &cobra.Command{
		Use:   "release",
		Short: "Sign and verify release checksum files",
		Long: `Sign and verify release checksum files (SHA256SUMS, checksums.txt).

A signature is a hybrid of Ed25519 and ML-DSA-65 and is valid only when both
verify. verify checks against the keys built into this binary.`,
	}
	keygenCmd.Flags().StringVarP(&keygenName, "name", "n", "", "key name, one word (required)")
	keygenCmd.Flags().StringVarP(&keygenOut, "out", "o", "", "private key file to create (required)")
	signCmd.Flags().StringVarP(&signKeyFile, "key", "k", "", "private key file (required)")
	signCmd.Flags().StringVarP(&signOut, "out", "o", "", "signature file (default FILE.sig)")
	verifyCmd.Flags().StringVarP(&verifySig, "sig", "s", "", "signature file (default FILE.sig)")
	verifyCmd.Flags().StringSliceVar(&verifyPubKey, "pubkey", nil, "also trust this public key file")
	root.AddCommand(keygenCmd, signCmd, verifyCmd)
	return root
}

var keygenCmd = &cobra.Command{
	Use:   "keygen",
	Short: "Create a signing key and print its public key",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if keygenName == "" || keygenOut == "" {
			return errors.New("--name and --out are required")
		}
		key, err := releasesig.KeyGenerate(keygenName)
		if err != nil {
			return err
		}
		privateText, err := key.MarshalText()
		if err != nil {
			return err
		}
		file, err := os.OpenFile(keygenOut, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // operator-chosen path
		if err != nil {
			return err
		}
		if _, err := file.Write(privateText); err != nil {
			file.Close() //nolint:errcheck,gosec
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		publicText, err := key.Public().MarshalText()
		if err != nil {
			return err
		}
		return cliout.Print(cmd, keygenResult{Name: key.Name, PublicKey: string(publicText)})
	},
}

var signCmd = &cobra.Command{
	Use:   "sign FILE",
	Short: "Write FILE.sig for FILE",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		if signKeyFile == "" {
			return errors.New("--key is required")
		}
		keyText, err := os.ReadFile(signKeyFile) //nolint:gosec // operator-chosen path
		if err != nil {
			return err
		}
		key, err := releasesig.PrivateKeyParse(keyText)
		if err != nil {
			return err
		}
		message, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		signature, err := key.Sign(message)
		if err != nil {
			return err
		}
		out := signOut
		if out == "" {
			out = args[0] + ".sig"
		}
		return os.WriteFile(out, signature, 0o644) //nolint:gosec // signatures are public
	},
}

var verifyCmd = &cobra.Command{
	Use:   "verify FILE",
	Short: "Verify FILE against FILE.sig and print the signing key name",
	Long: `Verify FILE against its signature with the release keys built into this
binary. Exits non-zero when the signature is missing, unknown or invalid.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		trusted, err := releasesig.TrustedKeys()
		if err != nil {
			return err
		}
		for _, path := range verifyPubKey {
			text, err := os.ReadFile(path) //nolint:gosec // operator-chosen path
			if err != nil {
				return err
			}
			key, err := releasesig.PublicKeyParse(text)
			if err != nil {
				return err
			}
			trusted = append(trusted, key)
		}
		message, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		sigPath := verifySig
		if sigPath == "" {
			sigPath = args[0] + ".sig"
		}
		signature, err := os.ReadFile(sigPath) //nolint:gosec // operator-chosen path
		if err != nil {
			return err
		}
		name, err := releasesig.Verify(message, signature, trusted)
		if err != nil {
			return err
		}
		return cliout.Print(cmd, verifyResult{Key: name})
	},
}

// keygenResult is the output of keygen. The private key is never printed.
type keygenResult struct {
	Name      string `json:"name"`
	PublicKey string `json:"public_key"`
}

// Human prints the public key file.
func (r keygenResult) Human(w io.Writer) error {
	_, err := io.WriteString(w, r.PublicKey)
	return err
}

// verifyResult is the output of verify.
type verifyResult struct {
	Key string `json:"key"`
}

// Human prints the name of the key that signed.
func (r verifyResult) Human(w io.Writer) error {
	_, err := fmt.Fprintln(w, r.Key)
	return err
}
