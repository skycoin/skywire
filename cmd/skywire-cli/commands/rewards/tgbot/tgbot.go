// Package clirewardstgbot cmd/skywire-cli/commands/rewards/tgbot/tgbot.go
package clirewardstgbot

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"
	tele "gopkg.in/telebot.v3"
)

var filePath string

func init() {

	RootCmd.Flags().StringVarP(&filePath, "watch", "w", "../reward/rewards/transactions0.txt", "File to watch - file where reward transaction IDs are recorded")

}

// Root command contains the reward telegram bot command
var RootCmd = &cobra.Command{
	Use:   "bot",
	Short: "reward notification telegram bot",
	Long:  "reward notification telegram bot",
	Run: func(_ *cobra.Command, _ []string) {
		chatIDStr := os.Getenv("TG_CHAT_ID")
		chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
		if err != nil {
			log.Fatalf("failed to parse chat ID: %v", err)
		}
		pref := tele.Settings{
			Token:  os.Getenv("TG_BOT_TOKEN"),
			Poller: &tele.LongPoller{Timeout: 10 * time.Second},
		}

		b, err := tele.NewBot(pref)
		if err != nil {
			log.Fatal(err)
			return
		}

		tgbotscript := `#!/bin/bash
		_stats() {
		#  [[ $1 == "" ]] && return
		  cat ` + strings.TrimSuffix(filePath, "/transactions0.txt") + `/hist/$(find ` + strings.TrimSuffix(filePath, "/transactions0.txt") + `/hist/ -name "*.txt" -type f -exec grep -l "$1" {} + | xargs -I{} basename {} | tr -d ".txt")_stats.txt
		}
		`

		//	var lastModTime time.Time
		lastModTime, err := os.Stat(filePath)
		if err != nil {
			log.Fatal(err)
			return
		}
		// Use a goroutine to periodically check the file for changes
		go func() {
			for {
				time.Sleep(2 * time.Second)

				fileInfo, err := os.Stat(filePath)
				if err != nil {
					log.Printf("Error checking file info: %s", err)
					continue
				}

				if fileInfo.ModTime().After(lastModTime.ModTime()) {
					// The file has been modified since the last check, get the last line which is the most recent txid
					lastLine, err := script.File(filePath).Last(1).String()
					if err != nil {
						log.Printf("Error getting last line of file: %v", err)
						continue
					}
					if lastLine != "" {
						tmpFile, err := os.CreateTemp(os.TempDir(), "*.sh")
						if err != nil {
							return
						}
						if err := tmpFile.Close(); err != nil {
							return
						}
						_, _ = script.Exec(`chmod +x ` + tmpFile.Name()).String()                                                //nolint
						_, _ = script.Echo(tgbotscript).WriteFile(tmpFile.Name())                                                //nolint
						stats, err := script.Exec(`bash -c 'source ` + tmpFile.Name() + `  ; _stats ` + lastLine + `'`).String() //nolint
						if err != nil {
							log.Printf("Error getting statistics: %v", err)
							continue
						}
						os.Remove(tmpFile.Name()) //nolint

						dateforlink, err := script.Echo(stats).First(1).Replace("date: ", "").String()
						if err != nil {
							log.Printf("Error getting date for link: %v", err)
							continue
						}
						msg := fmt.Sprintf("Rewards have been distributed!\n\nhttps://explorer.skycoin.com/app/transaction/%s\n\n%s\n\nhttps://fiber.skywire.dev/skycoin-rewards/hist/%s", lastLine, stats, dateforlink)
						// Send the last line to the Telegram chat
						_, err = b.Send(&tele.Chat{ID: chatID}, msg)
						if err != nil {
							log.Printf("Error sending message to Telegram chat: %s", err)
							continue
						}
					}

					lastModTime = fileInfo
				}
			}
		}()

		b.Start()
	},
}
