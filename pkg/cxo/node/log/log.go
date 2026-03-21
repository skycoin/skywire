package log

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
)

// defaults
const (
	Prefix string = ""      // default prefix
	Debug  bool   = false   // don't show debug logs by default
	All    Pin    = ^Pin(0) // default Debug pins (all pins)
	No     Pin    = 0       // no pins
)

// A Pin of a debug log
type Pin uint

// A Config represents configuration for logger
type Config struct {
	Prefix string    // log prefix
	Debug  bool      // show debug logs
	Pins   Pin       // debug pins
	Output io.Writer // provide an output
}

// NewConfig returns Config with default values
func NewConfig() (c Config) {
	c.Prefix = Prefix
	c.Debug = Debug
	c.Pins = All
	return
}

// FromFlags parses commandline flags. So, you need to call
// flag.Parse after this method. There is -log-prefix flag.
// And also, it provides -debug flag and -debug-pins. If
// the debug flag set to false, then -debug-pins ignored and
// the pins set to No.
func (c *Config) FromFlags() {

	flag.StringVar(&c.Prefix,
		"log-prefix",
		c.Prefix,
		"provide log-prefix")

	flag.BoolVar(&c.Debug,
		"debug",
		c.Debug,
		"print debug logs")

	var pins uint

	flag.UintVar(&pins,
		"debug-pins",
		uint(c.Pins),
		"debug pins (default all)")

	c.Pins = Pin(pins)
}

// A Logger is similar to log.Logger with Debug methods.
// The Debug methods uses Pin to show or not to show a message.
// Provided Pin compared with configured using logical AND.
// If result is not 0, then message will be printed.
// Thus you can separate debug messages printing
// only messages you want. For example:
//
//	const (
//	    LogTransport log.Pin = 1 < iota
//	    LogMessages
//	    LogSoemthing
//	)
//
//	c := NewConfig()
//	c.Prefix = "[node] "
//	c.Debug  = true       // enable debug messages
//	c.Pins   = log.All    // show all debug messages
//
//	//
//	// show all debug logs
//	//
//
//	l := NewLogger(c)
//
//	l.Debug(LogTransport, "new connection from 127.0.0.1:9090")
//	l.Debug(LogMessage, "message X received")
//	l.Debug(LogSomething, "something happens")
//
//	// [node] new connection from 127.0.0.1:9090
//	// [node] message X received
//	// [node] something happens
//
//	//
//	// show only transport and "something" logs
//	//
//
//	// ...
//
//	c.Pins = LogTransport | LogSomething
//
//	l := NewLogger(c)
//
//	// ...
//
//	// [node] new connection from 127.0.0.1:9090
//	// [node] something happens
//
// This way you can provide detailed logs without caring
// about big output. This feature applies only to debug logs.
// Set Debug field of the Config to turn all debug logs off
type Logger interface {
	// Pins of the Logger
	Pins() Pin

	SetPrefix(string)    // set prefix of underlying log.Logger
	SetFlags(int)        // set Flags of undelrying log.Logger
	SetOutput(io.Writer) // set Output of underlying log.Logger

	Print(...interface{})          //
	Println(...interface{})        //
	Printf(string, ...interface{}) //

	Panic(...interface{})          //
	Panicln(...interface{})        //
	Panicf(string, ...interface{}) //

	Fatal(...interface{})          //
	Fatalln(...interface{})        //
	Fatalf(string, ...interface{}) //

	Debug(pin Pin, args ...interface{})                 //
	Debugln(pin Pin, args ...interface{})               //
	Debugf(pin Pin, format string, args ...interface{}) //

	Error(err error, args ...interface{})
	Errorln(err error, args ...interface{})
	Errorf(err error, format string, args ...interface{})
}

type logger struct {
	*log.Logger
	pins Pin
}

// NewLogger create new Logger using given Config.
// By default flags of the Logger is log.Lshortfile|log.Ltime
func NewLogger(c Config) Logger {
	if c.Debug == false { //nolint:staticcheck
		c.Pins = No // don't show debug logs
	}
	if c.Output == nil {
		c.Output = os.Stderr
	}
	return &logger{
		Logger: log.New(c.Output, c.Prefix, log.Lshortfile|log.Ltime),
		pins:   c.Pins,
	}
}

func (l *logger) Debug(pin Pin, args ...interface{}) {
	if pin&l.pins != 0 {
		args = append([]interface{}{"[DBG] "}, args...)
		l.Output(2, fmt.Sprint(args...)) //nolint:errcheck,gosec
	}
}

func (l *logger) Debugln(pin Pin, args ...interface{}) {
	if pin&l.pins != 0 {
		args = append([]interface{}{"[DBG]"}, args...)
		l.Output(2, fmt.Sprintln(args...)) //nolint:errcheck,gosec
	}
}

func (l *logger) Debugf(pin Pin, format string, args ...interface{}) {
	if pin&l.pins != 0 {
		format = "[DBG] " + format
		l.Output(2, fmt.Sprintf(format, args...)) //nolint:errcheck,gosec
	}
}

func (l *logger) Error(err error, args ...interface{}) {
	errArgs := []interface{}{
		"[ERR] ",
		fmt.Sprintf("err=%s ", err),
	}
	l.Output(2, fmt.Sprint(append(errArgs, args...))) //nolint:errcheck,gosec
}

func (l *logger) Errorln(err error, args ...interface{}) {
	errArgs := []interface{}{
		"[ERR] ",
		fmt.Sprintf("err=%s ", err),
	}
	l.Output(2, fmt.Sprint(append(errArgs, args...))) //nolint:errcheck,gosec
}

func (l *logger) Errorf(err error, format string, args ...interface{}) {
	format = "[ERR] " + fmt.Sprintf("err=%s ", err) + format
	l.Output(2, fmt.Sprintf(format, args...)) //nolint:errcheck,gosec
}

func (l *logger) Pins() Pin {
	return l.pins
}
