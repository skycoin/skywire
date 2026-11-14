// Package rewardsbot pkg/deployment/rewards/bot/bot.go c3-app-rewards
//
// The telegram bot that announces each reward payout. `rewards bot` runs it
// alone, `rewards serve` and the embedded reward service run it beside the
// server.
package rewardsbot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bitfield/script"
	tele "gopkg.in/telebot.v3"

	"github.com/skycoin/skywire/pkg/logging"
)

// FromEnv reads the bot token and chat id from TG_BOT_TOKEN and TG_CHAT_ID.
func FromEnv() (token string, chatID int64, err error) {
	token = os.Getenv("TG_BOT_TOKEN")
	if token == "" {
		return "", 0, errors.New("TG_BOT_TOKEN is not set")
	}
	chatID, err = strconv.ParseInt(os.Getenv("TG_CHAT_ID"), 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("failed to parse chat ID: %w", err)
	}
	return token, chatID, nil
}

// Run posts the stats of each reward transaction appended to txFile, the
// transactions0.txt beside the hist directory, until ctx ends.
func Run(ctx context.Context, token string, chatID int64, txFile string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	// Errors from the bot API can carry its URL, which holds the token.
	scrub := func(err error) error {
		if err == nil {
			return nil
		}
		return errors.New(strings.ReplaceAll(err.Error(), token, "<token>"))
	}
	b, err := tele.NewBot(tele.Settings{Token: token})
	if err != nil {
		return scrub(err)
	}

	dir := filepath.Dir(txFile)
	tgbotscript := `#!/bin/bash
		_stats() {
		#  [[ $1 == "" ]] && return
		  cat ` + dir + `/hist/$(find ` + dir + `/hist/ -name "*.txt" -type f -exec grep -l "$1" {} + | xargs -I{} basename {} | tr -d ".txt")_stats.txt
		}
		`

	lastModTime, err := os.Stat(txFile)
	if err != nil {
		return err
	}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}

		fileInfo, err := os.Stat(txFile)
		if err != nil {
			botLog.Printf("Error checking file info: %s", err)
			continue
		}
		if !fileInfo.ModTime().After(lastModTime.ModTime()) {
			continue
		}
		// The file has been modified since the last check, get the last line which is the most recent txid
		lastLine, err := script.File(txFile).Last(1).String()
		if err != nil {
			botLog.Printf("Error getting last line of file: %v", err)
			continue
		}
		if lastLine != "" {
			tmpFile, err := os.CreateTemp(os.TempDir(), "*.sh")
			if err != nil {
				return err
			}
			if err := tmpFile.Close(); err != nil {
				return err
			}
			_, _ = script.Exec(`chmod +x ` + tmpFile.Name()).String() //nolint:errcheck
			_, _ = script.Echo(tgbotscript).WriteFile(tmpFile.Name()) //nolint:errcheck
			stats, err := script.Exec(`bash -c 'source ` + tmpFile.Name() + `  ; _stats ` + lastLine + `'`).String()
			//nolint:errcheck,gosec
			os.Remove(tmpFile.Name())
			if err != nil {
				botLog.Printf("Error getting statistics: %v", err)
				continue
			}

			dateforlink, err := script.Echo(stats).First(1).Replace("date: ", "").String()
			if err != nil {
				botLog.Printf("Error getting date for link: %v", err)
				continue
			}
			msg := fmt.Sprintf("Rewards have been distributed!\n\nhttps://explorer.skycoin.com/app/transaction/%s\n\n%s\n\nhttps://reward.skywire.dev/skycoin-rewards/hist/%s", lastLine, stats, dateforlink)
			if _, err = b.Send(&tele.Chat{ID: chatID}, msg); err != nil {
				botLog.Printf("Error sending message to Telegram chat: %s", scrub(err))
				continue
			}
		}
		lastModTime = fileInfo
	}
}

// Start runs the bot until ctx ends, starting it again a minute after
// it stops, since the bot only reports.
func Start(ctx context.Context, token string, chatID int64, txFile string, log *logging.Logger) {
	go func() {
		for {
			log.WithField("watch", txFile).Info("Telegram bot started")
			err := Run(ctx, token, chatID, txFile)
			if ctx.Err() != nil {
				return
			}
			log.WithError(err).Warn("Telegram bot stopped; starting it again in a minute")
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Minute):
			}
		}
	}()
}

// botLog is where the bot logs, as the bot command always did.
var botLog = logging.MustGetLogger("reward-bot")
