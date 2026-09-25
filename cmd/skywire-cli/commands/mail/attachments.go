// Package climail cmd/skywire-cli/commands/mail/attachments.go c4-vis-cli
package climail

import (
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/pkg/skymail"
)

var attachmentCmd = &cobra.Command{
	Use:   "attachment <id> <n>",
	Short: "Save attachment n of a message",
	Long: `Save attachment n of a message, numbered as "mail read" lists them.

It is written to a file named after the attachment in the current
directory unless -o names another; "-o -" writes it to stdout. With
--json the attachment is printed with its data base64-encoded.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		n, err := strconv.Atoi(args[1])
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("attachment number: %w", err))
		}
		a, err := mailClient(cmd).MailAttachment(folderOf(attSent), args[0], n)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		if isJSON, _ := cmd.Flags().GetBool(internal.JSONString); isJSON { //nolint:errcheck
			internal.PrintOutput(cmd.Flags(), a, "")
			return
		}
		out := attOut
		if out == "" {
			out = safeName(a.Name)
		}
		if out == "-" {
			_, _ = os.Stdout.Write(a.Data) //nolint:errcheck
			return
		}
		if err := os.WriteFile(out, a.Data, 0o600); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Printf("wrote %s (%d bytes)\n", out, len(a.Data))
	},
}

// safeName keeps only the last element of a name a sender chose, so an
// attachment cannot be written outside the current directory.
func safeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	if name == "" || name == "." || name == "/" || name == ".." {
		return "attachment"
	}
	return name
}

// readAttachments loads --attach files.
func readAttachments(files []string) ([]skymail.OutgoingAttachment, error) {
	var out []skymail.OutgoingAttachment
	for _, p := range files {
		data, err := os.ReadFile(p) //nolint:gosec // the user named it
		if err != nil {
			return nil, err
		}
		out = append(out, skymail.OutgoingAttachment{
			Name: filepath.Base(p), ContentType: mime.TypeByExtension(filepath.Ext(p)), Data: data,
		})
	}
	return out, nil
}
