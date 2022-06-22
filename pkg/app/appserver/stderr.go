package appserver

import (
	"bufio"
	"io"
	"strings"

	"github.com/sirupsen/logrus"
)

func printStdErr(stderr io.ReadCloser, errorLog *logrus.Entry) {
	cmdStderr := bufio.NewScanner(stderr)
	iErrs := getIgnoreErrs()
	go func() {
		for cmdStderr.Scan() {
			err := cmdStderr.Text()
			if !contains(iErrs, err) {
				errorLog.Error(cmdStderr.Text())
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
