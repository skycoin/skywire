package discord

import (
	"os"
	"time"

	"github.com/kz/discordrus"
	"github.com/sirupsen/logrus"
)

const webhookURLEnvName = "DISCORD_WEBHOOK_URL"

const (
	loggedLevel       = logrus.ErrorLevel
	startStopLogLevel = logrus.InfoLevel
)

const (
	// StartLogMessage defines a message on binary starting.
	StartLogMessage = "Starting"
	// StopLogMessage defines a message on binary stopping.
	StopLogMessage = "Stopping"
)

// Hook is a Discord logger hook.
type Hook struct {
	logrus.Hook
	limit      time.Duration
	timestamps map[string]time.Time
}

// Option defines an option for Discord logger hook.
type Option func(*Hook)

// WithLimit enables logger rate limiter with specified limit.
func WithLimit(limit time.Duration) Option {
	return func(h *Hook) {
		h.limit = limit
		h.timestamps = make(map[string]time.Time)
	}
}

// NewHook returns a new Hook.
func NewHook(tag, webHookURL string, opts ...Option) logrus.Hook {
	parent := discordrus.NewHook(webHookURL, loggedLevel, discordOpts(tag))

	hook := &Hook{
		Hook: parent,
	}

	for _, opt := range opts {
		opt(hook)
	}

	return hook
}

// Fire checks whether rate is fine and fires the underlying hook.
func (h *Hook) Fire(entry *logrus.Entry) error {
	switch entry.Message {
	case StartLogMessage, StopLogMessage:
		// Start and stop messages should be logged by Hook but they should have Info level.
		// With Info level, they would not be passed to hook.
		// So we can use Error level in the codebase and change level to Info in the hook,
		// then it appears as Info in logs.
		entry.Level = startStopLogLevel
	}

	if h.shouldFire(entry) {
		return h.Hook.Fire(entry)
	}

	return nil
}

func (h *Hook) shouldFire(entry *logrus.Entry) bool {
	if h.limit != 0 && h.timestamps != nil {
		v, ok := h.timestamps[entry.Message]
		if ok && entry.Time.Sub(v) < h.limit {
			return false
		}

		h.timestamps[entry.Message] = entry.Time
	}

	return true
}

func discordOpts(tag string) *discordrus.Opts {
	return &discordrus.Opts{
		Username:        tag,
		TimestampFormat: time.RFC3339,
		TimestampLocale: time.UTC,
	}
}

// GetWebhookURLFromEnv extracts webhook URL from an environment variable.
func GetWebhookURLFromEnv() string {
	return os.Getenv(webhookURLEnvName)
}
