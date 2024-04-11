// Package clilog cmd/skywire-cli/commands/log/clist.go
package clilog

import (
	"os"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"
)

func init() {
	tpCmd.Flags().StringVarP(&lcDir, "dir", "d", "", "path to surveys & transport bandwidth logging ")
}

var tpCmd = &cobra.Command{
	Use:   "tp",
	Short: "display collected transport bandwidth logging",
	Run: func(_ *cobra.Command, _ []string) {
		tmpFile, err := os.CreateTemp(os.TempDir(), "*.sh")
		if err != nil {
			return
		}
		defer os.Remove(tmpFile.Name())
		if err := tmpFile.Close(); err != nil {
			return
		}
		_, _ = script.Exec(`chmod +x ` + tmpFile.Name()).String()                                      //nolint
		_, _ = script.Echo(tplogscript).WriteFile(tmpFile.Name())                                      //nolint
		_, _ = script.Exec(`bash -c 'source ` + tmpFile.Name() + ` ; _tplogs ` + lcDir + `'`).Stdout() //nolint

	},
}

// TODO: translate to golang & remove external deps
const tplogscript = `#!/bin/bash
_tplogs() {
    echo "date tp_id,recv,sent,time_stamp"
    find "${1}"/*/ -type f -name "*.csv" -print | while read -r _i; do
        _date="${_i/.csv}"
        _date="${_date##*/}"
        while read -r _j; do
            echo "$_date $_j"
        done < "$_i"
    done | sort | uniq | tac | grep -v "tp_id,recv,sent,time_stamp" | grep -v " .$" | grep -E '^[^,]*,[^,]*,[^,]*,[^,]*$' | awk -F'[ ,]' '
        BEGIN { prev_tp_id = ""; print_next = 0 }
        {
            if (prev_tp_id != "" && $2 == prev_tp_id) {
                if ($4 == prev_recv && $3 == prev_sent) {
                    printf "\x1b[34m%s\x1b[0m\n", prev_line;
                    printf "\x1b[34m%s\x1b[0m\n", $0;
                    print_next = 0;
                } else {
                    printf "\x1b[33m%s\x1b[0m\n", prev_line;
                    printf "\x1b[33m%s\x1b[0m\n", $0;
                    print_next = 0;
                }
            } else {
                if (print_next == 1) {
                    print prev_line;
                }
                prev_line = $0;
                prev_tp_id = $2;
                prev_recv = $3;
                prev_sent = $4;
                print_next = 1;
            }
        }
        END {
            if (print_next == 1) {
                print prev_line;
            }
        }
    ' | sed '/,0,/ s/^/\x1b[31m/; s/$/\x1b[0m/'
}
`
