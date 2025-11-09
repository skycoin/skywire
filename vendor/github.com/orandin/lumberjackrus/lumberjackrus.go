package lumberjackrus

import (
	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
	"fmt"
)

type LogFile struct {
	// Filename is the file to write logs to.  Backup log files will be retained in the same directory.
	// It uses <processname>-lumberjack.log in os.TempDir() if empty.
	Filename string `json:"filename" yaml:"filename"`

	// MaxSize is the maximum size in megabytes of the log file before it gets rotated. It defaults to 100 megabytes.
	MaxSize int `json:"maxsize" yaml:"maxsize"`

	// MaxAge is the maximum number of days to retain old log files based on the timestamp encoded in their filename.
	//  Note that a day is defined as 24 hours and may not exactly correspond to calendar days due to daylight savings,
	// leap seconds, etc. The default is not to remove old log files based on age.
	MaxAge int `json:"maxage" yaml:"maxage"`

	// MaxBackups is the maximum number of old log files to retain.  The default is to retain all old log files (though
	// MaxAge may still cause them to get deleted.)
	MaxBackups int `json:"maxbackups" yaml:"maxbackups"`

	// LocalTime determines if the time used for formatting the timestamps in backup files is the computer's local time.
	// The default is to use UTC time.
	LocalTime bool `json:"localtime" yaml:"localtime"`

	// Compress determines if the rotated log files should be compressed using gzip.
	Compress bool `json:"compress" yaml:"compress"`
}

type LogFileOpts map[logrus.Level]*LogFile

type Hook struct {
	defaultLogger *lumberjack.Logger
	formatter     logrus.Formatter
	minLevel      logrus.Level
	loggerByLevel map[logrus.Level]*lumberjack.Logger
}

func NewHook(defaultLogger *LogFile, minLevel logrus.Level, formatter logrus.Formatter, opts *LogFileOpts) (*Hook, error) {

	if defaultLogger == nil {
		return nil, fmt.Errorf("default logger cannot be nil")
	}

	hook := Hook{
		defaultLogger: &lumberjack.Logger{
			Filename:   defaultLogger.Filename,
			MaxSize:    defaultLogger.MaxSize,
			MaxBackups: defaultLogger.MaxBackups,
			MaxAge:     defaultLogger.MaxAge,
			Compress:   defaultLogger.Compress,
			LocalTime:  defaultLogger.LocalTime,
		},
		minLevel:      minLevel,
		formatter:     formatter,
		loggerByLevel: make(map[logrus.Level]*lumberjack.Logger),
	}

	if opts != nil {

		maxLevel := len(hook.Levels())
		for level, config := range *opts {

			if maxLevel <= int(level) {
				continue
			}

			hook.loggerByLevel[level] = &lumberjack.Logger{
				Filename:   config.Filename,
				MaxSize:    config.MaxSize,
				MaxBackups: config.MaxBackups,
				MaxAge:     config.MaxAge,
				Compress:   config.Compress,
				LocalTime:  config.LocalTime,
			}
		}
	}

	return &hook, nil
}

func (hook *Hook) Fire(entry *logrus.Entry) error {

	msg, err := hook.formatter.Format(entry)
	if err != nil {
		return err
	}

	if logger, ok := hook.loggerByLevel[entry.Level]; ok {
		_, err = logger.Write([]byte(msg))
	} else {
		_, err = hook.defaultLogger.Write([]byte(msg))
	}

	return err
}

func (hook *Hook) Levels() []logrus.Level {
	return logrus.AllLevels[:hook.minLevel+1]
}
