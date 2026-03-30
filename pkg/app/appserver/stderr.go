// Package appserver pkg/app/appserver/stderr.go
package appserver

import (
	"bufio"
	"io"
	"strings"

	"github.com/sirupsen/logrus"
)

// printStdErr reads app stderr and logs non-suppressed lines as errors.
// Suppressed messages (see getIgnoreErrs) are harmless OS/library noise
// that cannot be eliminated at source (iptables lock, rpc shutdown, etc).
func printStdErr(stderr io.ReadCloser, errorLog *logrus.Entry) {
	cmdStderr := bufio.NewScanner(stderr)
	iErrs := getIgnoreErrs()
	go func() {
		for cmdStderr.Scan() {
			err := cmdStderr.Text()
			if !contains(iErrs, err) {
				if err != "" {
					errorLog.Error(err)
				}
			}
		}
	}()
}

func contains(iErrs []string, err string) bool {
	for _, iErr := range iErrs {
		if strings.Contains(err, iErr) {
			return true
		}
	}
	return false
}
