// Package main cmd/gen/gen.go
package main

import (
	"encoding/json"
	"log"
	"regexp"
	"runtime"

	"github.com/bitfield/script"
	cc "github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"
)

var (
	printJSON bool
	writeFile string
)

func init() {
	rootCmd.Flags().BoolVarP(&printJSON, "json", "j", false, "output json format")
	rootCmd.Flags().StringVarP(&writeFile, "output", "o", "", "write to the specified file (i.e. arches.json)")
}

// there is no other better way to get a list of architectures via a library or any simpler method at runtime
// this file is executed by go generate from the root directory of the repository github.com/skycoin/skywire
// go run cmd/gen/gen.go -j -o arches.json

var rootCmd = &cobra.Command{
	Use:   "gen",
	Short: "print architectures",
	Long:  "print architectures",
	Run: func(_ *cobra.Command, _ []string) {
		if writeFile == "" {
			switch runtime.GOOS {
			case "windows":
				writeFile = `\\.\NUL`
			default: // For macOS and Linux
				writeFile = "/dev/null"
			}
		}
		if !printJSON {
			//equivalent bash one-liner:
			//go tool dist list | awk -F '/' '{print $NF}' | awk '{$1=$1};1' | sort | uniq
			_, err := script.Exec(`go tool dist list`).ReplaceRegexp(regexp.MustCompile(".*/"), "").Freq().ReplaceRegexp(regexp.MustCompile(`^\s*\d+\s+`), "").Tee().WriteFile(writeFile)
			if err != nil {
				log.Fatal(err)
			}
		} else {
			rawOutput, err := script.Exec(`go tool dist list`).ReplaceRegexp(regexp.MustCompile(".*/"), "").Freq().ReplaceRegexp(regexp.MustCompile(`^\s*\d+\s+`), "").Slice()
			if err != nil {
				log.Fatal(err)
			}

			jsonData, err := json.Marshal(rawOutput)
			if err != nil {
				log.Fatal(err)
			}
			//equivalent bash one-liner:
			//go tool dist list | awk -F '/' '{print $NF}' | awk '{$1=$1};1' | sort | uniq | jq -R -s -c 'split("\n") | map(select(length > 0))' | tee arches.json
			_, err = script.Echo(string(jsonData) + "\n").Tee().WriteFile(writeFile)
			if err != nil {
				log.Fatal(err)
			}
		}
	},
}

func init() {
	var helpflag bool
	rootCmd.SetUsageTemplate(help)
	rootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help menu")
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().MarkHidden("help") //nolint
}

func main() {
	cc.Init(&cc.Config{
		RootCmd:         rootCmd,
		Headings:        cc.HiBlue + cc.Bold,
		Commands:        cc.HiBlue + cc.Bold,
		CmdShortDescr:   cc.HiBlue,
		Example:         cc.HiBlue + cc.Italic,
		ExecName:        cc.HiBlue + cc.Bold,
		Flags:           cc.HiBlue + cc.Bold,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	rootCmd.Execute()
}

const help = "{{if .HasAvailableSubCommands}}{{end}} {{if gt (len .Aliases) 0}}\r\n\r\n" +
	"{{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}" +
	"Available Commands:{{range .Commands}}  {{if and (ne .Name \"completion\") .IsAvailableCommand}}\r\n  " +
	"{{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}\r\n\r\n" +
	"Flags:\r\n" +
	"{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}\r\n\r\n" +
	"Global Flags:\r\n" +
	"{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}\r\n\r\n"
