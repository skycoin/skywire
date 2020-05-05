package appcommon

import (
	"io"

	"github.com/SkycoinProject/skycoin/src/util/logging"
)

// NewLogger returns a logger which persists app logs. This logger should be passed down
// for use on any other function used by the app. It's configured from an additional app argument.
// It modifies os.Args stripping from it such value. Should be called before using os.Args inside the app
func NewLogger(dbPath string, appName string) *logging.MasterLogger {
	db, err := newBoltDB(dbPath, appName)
	if err != nil {
		panic(err)
	}

	l := logging.NewMasterLogger()
	l.SetOutput(io.MultiWriter(l.Out, db))
	return l
}

// TimestampFromLog is an utility function for retrieving the timestamp from a log. This function should be modified
// if the time layout is changed
func TimestampFromLog(log string) string {
	return log[1:36]
}
