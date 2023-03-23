# lumberjackrus | local filesystem hook for Logrus

## Example

```golang
package main

import (
	"github.com/sirupsen/logrus"
	"github.com/orandin/lumberjackrus"
)

func init() {
	logrus.SetFormatter(&logrus.TextFormatter{})
	logrus.SetLevel(logrus.DebugLevel)

	hook, err := lumberjackrus.NewHook(
		&lumberjackrus.LogFile{
			Filename:   "/tmp/general.log",
			MaxSize:    100,
			MaxBackups: 1,
			MaxAge:     1,
			Compress:   false,
			LocalTime:  false,
		},
		logrus.InfoLevel,
		&logrus.TextFormatter{},
		&lumberjackrus.LogFileOpts{
			logrus.InfoLevel: &lumberjackrus.LogFile{
				Filename: "/tmp/info.log",
			},
			logrus.ErrorLevel: &lumberjackrus.LogFile{
				Filename:   "/tmp/error.log",
				MaxSize:    100,   // optional
				MaxBackups: 1,     // optional
				MaxAge:     1,     // optional
				Compress:   false, // optional
				LocalTime:  false, // optional
			},
		},
	)

	if err != nil {
		panic(err)
	}

	logrus.AddHook(hook)
}

func main() {
	logrus.Debug("Debug message") // It is not written to a file (because debug level < minLevel)
	logrus.Info("Info message")   // Written in /tmp/info.log
	logrus.Warn("Warn message")   // Written in /tmp/general.log
	logrus.Error("Error message") // Written in /tmp/error.log
}
```
