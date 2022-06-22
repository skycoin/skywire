package appserver

import (
	"bufio"
	"io"
	"strings"

	"github.com/sirupsen/logrus"
)

func printStdErr(stderr io.ReadCloser, errorLog *logrus.Entry) {
	cmdStderr := bufio.NewScanner(stderr)
	ignoreErrs := getIgnoreErrs()
	go func() {
		for cmdStderr.Scan() {
			err := cmdStderr.Text()
			if _, ok := ignoreErrs[err]; !ok {
				if !strings.Contains(err, "rpc.Serve: accept:accept") {
					errorLog.Error(cmdStderr.Text())
				}
			}
		}
	}()
}
