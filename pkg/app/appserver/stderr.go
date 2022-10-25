// Package appserver pkg/app/appserver/stderr.go
package appserver

import (
	"bufio"
	"io"
	"strings"

	"github.com/sirupsen/logrus"
)

// TODO(ersonp): check if we can get rid of the errors altogether instead of ignoring/suppressing them.

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
