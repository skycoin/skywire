package cmdutil

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log/syslog"
	"net/http"
	"os"
	"strings"
	"unicode"

	"github.com/sirupsen/logrus"
	logrussyslog "github.com/sirupsen/logrus/hooks/syslog"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/discord"
	"github.com/skycoin/dmsg/promutil"
)

// Associated errors.
var (
	ErrTagCannotBeEmpty           = errors.New("tag cannot be empty")
	ErrTagHasInvalidChars         = errors.New("tag can only contain alphanumeric values and underscore")
	ErrTagHasMisplacedUnderscores = errors.New("tag cannot start or end with an underscore or have two underscores back-to-back")
	ErrInvalidLogString           = errors.New("failed to convert string to log level")
	ErrInvalidSyslogNet           = errors.New("network type is unsupported for syslog")
)

const (
	stdinConfig = "stdin"
)

// ServiceFlags represents common flags which are shared across services.
type ServiceFlags struct {
	MetricsAddr string
	Syslog      string
	SyslogNet   string
	LogLevel    string
	Tag         string
	Config      string
	Stdin       bool

	// state
	checkDone   bool
	loggerDone  bool
	metricsDone bool

	logger  *logging.Logger
	metrics promutil.HTTPMetrics
}

// Init initiates the service flags.
// The following are performed:
// 	* Ensure 'defaultTag' is provided and valid.
// 	* Set "library" defaults.
// 	* Set "exec" defaults - provided by 'defaultTag' and 'defaultConf'.
// 	* Add flags to 'rootCmd'.
func (sf *ServiceFlags) Init(rootCmd *cobra.Command, defaultTag, defaultConf string) {
	if err := ValidTag(defaultTag); err != nil {
		panic(err)
	}

	// "library" defaults
	if sf.SyslogNet == "" {
		// TODO (evanlinjin): Consider using tcp as syslog udp is legacy.
		sf.SyslogNet = "udp"
	}
	if sf.LogLevel == "" {
		sf.LogLevel = "debug"
	}

	// "exec" defaults
	if defaultTag != "" {
		sf.Tag = defaultTag
	}
	if defaultConf != "" {
		sf.Config = defaultConf
	}

	// flags
	rootCmd.Flags().StringVarP(&sf.MetricsAddr, "metrics", "m", sf.MetricsAddr, "address to serve metrics API from")
	rootCmd.Flags().StringVar(&sf.Syslog, "syslog", sf.Syslog, "address in which to dial to syslog server")
	rootCmd.Flags().StringVar(&sf.SyslogNet, "syslog-net", sf.SyslogNet, "network in which to dial to syslog server")
	rootCmd.Flags().StringVar(&sf.LogLevel, "syslog-lvl", sf.LogLevel, "minimum log level to report")
	rootCmd.Flags().StringVar(&sf.Tag, "tag", sf.Tag, "tag used for logging and metrics")

	// only enable config flags if 'defaultConf' is set
	if defaultConf != "" {
		rootCmd.Flags().StringVarP(&sf.Config, "config", "c", sf.Config, "location of config file (STDIN to read from standard input)")
		rootCmd.Flags().BoolVar(&sf.Stdin, "stdin", sf.Stdin, "whether to read config via stdin")
	}
}

// Check checks service flags.
func (sf *ServiceFlags) Check() error {
	if alreadyDone(&sf.checkDone) {
		return nil
	}

	if sf.Syslog != "" {
		switch sf.SyslogNet {
		case "tcp", "udp", "unix":
		default:
			return fmt.Errorf("%w: %s", ErrInvalidSyslogNet, sf.SyslogNet)
		}
	}

	if _, _, err := LevelFromString(sf.LogLevel); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidLogString, sf.LogLevel)
	}

	if err := ValidTag(sf.Tag); err != nil {
		return fmt.Errorf("%w: %s", err, sf.Tag)
	}

	return nil
}

// Logger returns the logger as specified by the service flags.
func (sf *ServiceFlags) Logger() *logging.Logger {
	if alreadyDone(&sf.loggerDone) {
		return sf.logger
	}

	log := logging.MustGetLogger(sf.Tag)
	sf.logger = log

	logLvl, sysLvl, err := LevelFromString(sf.LogLevel)
	if err != nil {
		panic(err) // should not happen as we have already checked earlier on
	}
	logging.SetLevel(logLvl)

	if sf.Syslog != "" {
		hook, err := logrussyslog.NewSyslogHook(sf.SyslogNet, sf.Syslog, sysLvl, sf.Tag)
		if err != nil {
			log.WithError(err).
				WithField("net", sf.SyslogNet).
				WithField("addr", sf.Syslog).
				Fatal("Failed to connect to syslog daemon.")
		}
		logging.AddHook(hook)
	}

	if discordWebhookURL := discord.GetWebhookURLFromEnv(); discordWebhookURL != "" {
		hook := discord.NewHook(sf.Tag, discordWebhookURL)
		logging.AddHook(hook)
	}

	return log
}

// ParseConfig parses config from service tags.
// If checkArgs is set, we additionally parse os.Args to find a config path.
func (sf *ServiceFlags) ParseConfig(args []string, checkArgs bool, v interface{}) error {
	r, err := sf.obtainConfigReader(args, checkArgs)
	if err != nil {
		return err
	}
	defer func() {
		if err := r.Close(); err != nil {
			sf.logger.WithError(err).Warn("Failed to close config source.")
		}
	}()

	b, err := ioutil.ReadAll(r)
	if err != nil {
		return fmt.Errorf("failed to read from config source: %w", err)
	}

	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("failed to decode config file: %w", err)
	}

	j, err := json.MarshalIndent(v, "", "\t")
	if err != nil {
		panic(err) // should not happen
	}
	sf.logger.Infof("Read config: %s", string(j))

	return nil
}

func (sf *ServiceFlags) obtainConfigReader(args []string, checkArgs bool) (io.ReadCloser, error) {
	switch {
	case sf.Stdin || strings.ToLower(sf.Config) == stdinConfig:
		stdin := ioutil.NopCloser(os.Stdin) // ensure stdin is not closed
		return stdin, nil

	case checkArgs:
		if len(args) == 1 {
			break
		}

		for i, arg := range args {
			if strings.HasSuffix(arg, ".json") && i > 0 && !strings.HasPrefix(args[i-1], "-") {
				f, err := os.Open(arg) //nolint:gosec
				if err != nil {
					return nil, fmt.Errorf("failed to open config file: %w", err)
				}
				return f, nil
			}
		}

	case sf.Config != "":
		f, err := os.Open(sf.Config)
		if err != nil {
			return nil, fmt.Errorf("failed to open config file: %w", err)
		}
		return f, nil
	}

	return nil, errors.New("no config location specified")
}

// HTTPMetrics returns a HTTPMetrics implementation based on service flags.
func (sf *ServiceFlags) HTTPMetrics() promutil.HTTPMetrics {
	if alreadyDone(&sf.metricsDone) {
		return sf.metrics
	}

	if sf.MetricsAddr == "" {
		m := promutil.NewEmptyHTTPMetrics()
		sf.metrics = m

		return m
	}

	m := promutil.NewHTTPMetrics(sf.Tag)
	sf.metrics = m

	mux := http.NewServeMux()
	promutil.AddMetricsHandle(mux, m.Collectors()...)

	addr := sf.MetricsAddr
	sf.logger.WithField("addr", addr).Info("Serving metrics.")
	go func() { sf.logger.Fatal(http.ListenAndServe(addr, mux)) }()

	return m
}

// ValidTag returns an error if the tag is invalid.
func ValidTag(tag string) error {
	if tag == "" {
		return ErrTagCannotBeEmpty
	}

	// check: valid characters
	for _, c := range tag {
		ranges := []*unicode.RangeTable{unicode.Letter, unicode.Number}
		if unicode.IsOneOf(ranges, c) || c == '_' {
			continue
		}
		return ErrTagHasInvalidChars
	}

	// check: correct positioning of characters
	for i, c := range tag {
		if i == 0 || i == len(tag)-1 {
			if c == '_' {
				return ErrTagHasMisplacedUnderscores
			}
			continue
		}
		if c == '_' && (tag[i-1] == '_' || tag[i+1] == '_') {
			return ErrTagHasMisplacedUnderscores
		}
	}

	return nil
}

// LevelFromString returns a logrus.Level and syslog.Priority from a string identifier.
func LevelFromString(s string) (logrus.Level, syslog.Priority, error) {
	switch strings.ToLower(s) {
	case "debug":
		return logrus.DebugLevel, syslog.LOG_DEBUG, nil
	case "info", "notice":
		return logrus.InfoLevel, syslog.LOG_INFO, nil
	case "warn", "warning":
		return logrus.WarnLevel, syslog.LOG_WARNING, nil
	case "error":
		return logrus.ErrorLevel, syslog.LOG_ERR, nil
	case "fatal", "critical":
		return logrus.FatalLevel, syslog.LOG_CRIT, nil
	case "panic":
		return logrus.PanicLevel, syslog.LOG_EMERG, nil
	default:
		return logrus.DebugLevel, syslog.LOG_DEBUG, ErrInvalidLogString
	}
}

func alreadyDone(done *bool) bool {
	if *done {
		return true
	}
	*done = true
	return false
}
