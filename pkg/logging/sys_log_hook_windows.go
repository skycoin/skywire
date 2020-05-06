//+build windows

package logging

import (
	"errors"

	"github.com/sirupsen/logrus"
)

func NewSysLogHook(string, string, interface{}, string) (logrus.Hook, error) {
	return nil, errors.New("syslog is not supported for this OS")
}
