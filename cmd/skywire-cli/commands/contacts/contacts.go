// Package clicontacts cmd/skywire-cli/commands/contacts/contacts.go c4-vis-cli
//
// `skywire cli contacts` — the visor's address book (pkg/contacts).
package clicontacts

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/contacts"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

var (
	cName   string
	cKeys   []string
	cMail   []string
	cPay    []string
	cGroups []string
	cNote   string
	cOut    string
)

func init() {
	for _, c := range []*cobra.Command{addCmd, setCmd} {
		c.Flags().StringSliceVar(&cKeys, "key", nil, "a skywire public key (repeatable)")
		c.Flags().StringSliceVar(&cMail, "mail", nil, "a mail address (repeatable)")
		c.Flags().StringSliceVar(&cPay, "pay", nil, "a payment address as coin:address, e.g. skycoin:2jBbGx (repeatable)")
		c.Flags().StringSliceVar(&cGroups, "group", nil, "a group (repeatable)")
		c.Flags().StringVar(&cNote, "note", "", "notes")
	}
	setCmd.Flags().StringVar(&cName, "name", "", "a new name")
	exportCmd.Flags().StringVarP(&cOut, "out", "o", "", "write to this file instead of stdout")
	RootCmd.AddCommand(lsCmd, showCmd, addCmd, setCmd, rmCmd, importCmd, exportCmd)
}

// RootCmd is `skywire cli contacts`, which lists the book when bare.
var RootCmd = &cobra.Command{
	Use:   "contacts",
	Short: "The visor's address book",
	Long: "The visor's address book: the people its operator knows, with the skywire\n" +
		"keys, mail and payment addresses that reach them. Each contact is a vCard\n" +
		"4.0 file beside the visor's local directory, so the book imports from and\n" +
		"exports to any address book. Nothing in it is published. A contact is named\n" +
		"by its UID or its name. Bare `contacts` lists the book.",
	Example: `  skywire cli contacts add Alice --key 02a4... --pay skycoin:2jBbGx --group friends
  skywire cli contacts set Alice --mail alice@example.com
  skywire cli contacts export -o contacts.vcf
  skywire cli contacts import phone.vcf`,
	Run: func(cmd *cobra.Command, _ []string) { list(cmd) },
}

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List the contacts",
	Run:   func(cmd *cobra.Command, _ []string) { list(cmd) },
}

var showCmd = &cobra.Command{
	Use:   "show <name|uid>",
	Short: "Show one contact",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c := find(cmd, api(cmd), args[0])
		internal.PrintOutput(cmd.Flags(), c, describe(c))
	},
}

var addCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a contact",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		c := contacts.Contact{Name: args[0]}
		apply(cmd, &c)
		put(cmd, api(cmd), c, "added")
	},
}

var setCmd = &cobra.Command{
	Use:   "set <name|uid>",
	Short: "Change a contact: each flag given replaces that field",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpc := api(cmd)
		c := find(cmd, rpc, args[0])
		if cmd.Flags().Changed("name") {
			c.Name = cName
		}
		apply(cmd, &c)
		put(cmd, rpc, c, "updated")
	},
}

var rmCmd = &cobra.Command{
	Use:   "rm <name|uid>",
	Short: "Remove a contact",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		rpc := api(cmd)
		c := find(cmd, rpc, args[0])
		if err := rpc.ContactsDelete(c.UID); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), c.UID, fmt.Sprintf("%s removed\n", c.Name))
	},
}

var importCmd = &cobra.Command{
	Use:   "import <file.vcf|->",
	Short: "Add every card of a .vcf file, replacing contacts with the same UID",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var data []byte
		var err error
		if args[0] == "-" {
			data, err = io.ReadAll(os.Stdin)
		} else {
			data, err = os.ReadFile(args[0])
		}
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		n, err := api(cmd).ContactsImport(data)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), n, fmt.Sprintf("%d contact(s) imported\n", n))
	},
}

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Write the whole book as one .vcf file",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		data, err := api(cmd).ContactsExport()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if cOut == "" {
			_, _ = os.Stdout.Write(data) //nolint:errcheck
			return
		}
		if err := os.WriteFile(cOut, data, 0o600); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		internal.PrintOutput(cmd.Flags(), cOut, fmt.Sprintf("written to %s\n", cOut))
	},
}

func api(cmd *cobra.Command) visorapi.API {
	rpc, err := clirpc.Client(cmd.Flags())
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	return rpc
}

func list(cmd *cobra.Command) {
	cs, err := api(cmd).ContactsList()
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	if cs == nil {
		cs = []contacts.Contact{}
	}
	if len(cs) == 0 {
		internal.PrintOutput(cmd.Flags(), cs, "No contacts.\n")
		return
	}
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tKEYS\tMAIL\tPAY\tGROUPS\tUID") //nolint:errcheck,gosec
	for _, c := range cs {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\t%s\n", c.Name, len(c.Keys), len(c.Mail), len(c.Payments), //nolint:errcheck,gosec
			dash(strings.Join(c.Groups, ",")), c.UID)
	}
	tw.Flush() //nolint:errcheck,gosec
	internal.PrintOutput(cmd.Flags(), cs, b.String())
}

// find resolves a UID, or a name matched without case. An ambiguous name is an
// error that lists the UIDs to choose from.
func find(cmd *cobra.Command, rpc visorapi.API, ref string) contacts.Contact {
	cs, err := rpc.ContactsList()
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	var byName []contacts.Contact
	for _, c := range cs {
		if c.UID == ref {
			return c
		}
		if strings.EqualFold(c.Name, ref) {
			byName = append(byName, c)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0]
	case 0:
		internal.PrintFatalError(cmd.Flags(), fmt.Errorf("no contact %q", ref))
	}
	uids := make([]string, len(byName))
	for i, c := range byName {
		uids[i] = c.UID
	}
	internal.PrintFatalError(cmd.Flags(), fmt.Errorf("%d contacts are named %q; give a UID: %s", len(byName), ref, strings.Join(uids, ", ")))
	return contacts.Contact{}
}

// apply sets each field whose flag was given.
func apply(cmd *cobra.Command, c *contacts.Contact) {
	f := cmd.Flags()
	if f.Changed("key") {
		c.Keys = nil
		for _, s := range cKeys {
			var pk cipher.PubKey
			if err := pk.Set(strings.TrimSpace(s)); err != nil {
				internal.PrintFatalError(f, fmt.Errorf("key %q: %w", s, err))
			}
			c.Keys = append(c.Keys, pk)
		}
	}
	if f.Changed("pay") {
		c.Payments = nil
		for _, s := range cPay {
			p, err := contacts.ParsePayment(s)
			if err != nil {
				internal.PrintFatalError(f, err)
			}
			c.Payments = append(c.Payments, p)
		}
	}
	setIf(f, "mail", &c.Mail, cMail)
	setIf(f, "group", &c.Groups, cGroups)
	if f.Changed("note") {
		c.Notes = cNote
	}
}

func setIf(f *pflag.FlagSet, name string, field *[]string, v []string) {
	if f.Changed(name) {
		*field = v
	}
}

func put(cmd *cobra.Command, rpc visorapi.API, c contacts.Contact, verb string) {
	stored, err := rpc.ContactsPut(c)
	if err != nil {
		internal.PrintFatalError(cmd.Flags(), err)
	}
	internal.PrintOutput(cmd.Flags(), stored, fmt.Sprintf("%s %s (%s)\n", stored.Name, verb, stored.UID))
}

func describe(c contacts.Contact) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nuid: %s\n", c.Name, c.UID)
	for _, k := range c.Keys {
		fmt.Fprintf(&b, "key: %s\n", k.Hex())
	}
	for _, m := range c.Mail {
		fmt.Fprintf(&b, "mail: %s\n", m)
	}
	for _, p := range c.Payments {
		fmt.Fprintf(&b, "pay: %s\n", p.URI())
	}
	if len(c.Groups) > 0 {
		fmt.Fprintf(&b, "groups: %s\n", strings.Join(c.Groups, ", "))
	}
	if c.Notes != "" {
		fmt.Fprintf(&b, "notes: %s\n", c.Notes)
	}
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
