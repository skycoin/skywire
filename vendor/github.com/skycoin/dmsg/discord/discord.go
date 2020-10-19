package discord

import (
	"os"
	"time"

	"github.com/kz/discordrus"
	"github.com/sirupsen/logrus"
)

const (
	webhookURLEnvName = "DISCORD_WEBHOOK_URL"
)

// NewHook creates a new Discord hook.
func NewHook(tag, webHookURL string) logrus.Hook {
	return discordrus.NewHook(webHookURL, logrus.ErrorLevel, discordOpts(tag))
}

func discordOpts(tag string) *discordrus.Opts {
	return &discordrus.Opts{
		Username:        tag,
		TimestampFormat: time.RFC3339,
		TimestampLocale: time.UTC,
	}
}

// GetWebhookURLFromEnv extracts Discord webhook URL from environment variables.
func GetWebhookURLFromEnv() string {
	return os.Getenv(webhookURLEnvName)
}
