// Package mgmt cmd/skywire-cli/commands/mgmt/api.go
package mgmt

var (
	remotePK string
)

func init() {
	RootCmd.PersistentFlags().StringVarP(&remotePK, "pk", "k", "", "remote public key to connect to")
}
